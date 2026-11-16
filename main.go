package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	"github.com/rs/zerolog/log"
)

var dryRun = os.Getenv("DRY_RUN") == "true"

func main() {
	config := MustLoadConfig()

	larkClient := NewLarkClient(config.Lark.AppId, config.Lark.AppSecret, config.Lark.AppToken, config.Lark.TableId)

	qfClient, err := NewQFlowClient(config.QFlow.AppId, config.QFlow.ViewId, config.QFlow.Cookies)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create QingFlow client")
	}

	if dryRun {
		defer qfClient.Close()
		log.Warn().Msg("Dry run mode")
		resp, err := qfClient.GetValue(context.Background(), 1, 1)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get value")
		}

		records := qfClient.Map(resp)
		if len(records) == 0 {
			return
		}
		m := records[0]
		j, _ := json.MarshalIndent(m, "", "  ")
		log.Info().Msgf("Dry run data: \n%s", j)
		return
	}

	log.Info().Msg("Starting job")
	success := syncRecords(context.Background(), qfClient, larkClient)
	_ = qfClient.Close()
	if !success {
		os.Exit(1)
	}
}

func syncRecords(ctx context.Context, qfClient *QFlowClient, larkClient *LarkClient) bool {
	started := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	indexResult := make(chan struct {
		index map[string][]string
		err   error
	}, 1)
	go func() {
		index, err := larkClient.RecordIndex(ctx)
		indexResult <- struct {
			index map[string][]string
			err   error
		}{index, err}
	}()

	records, err := qfClient.AllRecords(ctx)
	if err != nil {
		cancel()
		<-indexResult
		log.Error().Err(err).Msg("Failed to read QingFlow")
		return false
	}
	destination := <-indexResult
	if destination.err != nil {
		log.Error().Err(destination.err).Msg("Failed to index Feishu records")
		return false
	}
	log.Info().Int("indexed", len(destination.index)).Dur("elapsed", time.Since(started)).Msg("Indexed Feishu records")

	create, update, err := recordBuckets(records, destination.index)
	if err != nil {
		log.Error().Err(err).Int("not_run", len(records)).Msg("Cannot build Feishu batches")
		return false
	}
	log.Info().Int("create", len(create)).Int("update", len(update)).Msg("Prepared Feishu batches")
	created, failed := writeBatches(ctx, "batch_create", create, larkClient.CreateBatch)
	updated := 0
	if failed == 0 {
		updated, failed = writeBatches(ctx, "batch_update", update, larkClient.UpdateBatch)
	}
	notRun := len(records) - created - updated - failed
	log.Info().Int("expected", len(records)).Int("created", created).Int("updated", updated).
		Int("failed", failed).Int("not_run", notRun).Dur("elapsed", time.Since(started)).
		Bool("success", failed == 0).Msg("Sync summary")
	return failed == 0
}

func recordBuckets(records []map[string]string, index map[string][]string) (create, update []*larkbitable.AppTableRecord, err error) {
	numbers := make(map[string]bool, len(records))
	for i, item := range records {
		number := strings.TrimSpace(item[IDFieldName])
		if number == "" || numbers[number] {
			return nil, nil, fmt.Errorf("missing or duplicate QingFlow number at record %d", i+1)
		}
		numbers[number] = true
		matches := index[number]
		record := larkbitable.NewAppTableRecordBuilder().Fields(parseFields(item))
		switch len(matches) {
		case 0:
			create = append(create, record.Build())
		case 1:
			update = append(update, record.RecordId(matches[0]).Build())
		default:
			return nil, nil, fmt.Errorf("QingFlow record %d matches %d Feishu records", i+1, len(matches))
		}
	}
	return create, update, nil
}

func writeBatches(ctx context.Context, operation string, records []*larkbitable.AppTableRecord, write func(context.Context, []*larkbitable.AppTableRecord) error) (done, failed int) {
	const batchSize = 500
	for batch := 1; done < len(records); batch++ {
		size := batchSize
		if remaining := len(records) - done; remaining < size {
			size = remaining
		}
		started := time.Now()
		if err := write(ctx, records[done:done+size]); err != nil {
			log.Error().Err(err).Str("operation", operation).Int("batch", batch).
				Int("records", size).Dur("elapsed", time.Since(started)).Msg("Feishu batch failed")
			return done, size
		}
		log.Info().Str("operation", operation).Int("batch", batch).Int("records", size).
			Dur("elapsed", time.Since(started)).Msg("Feishu batch completed")
		done += size
	}
	return done, 0
}
