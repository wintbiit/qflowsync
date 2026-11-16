package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

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

		m := qfClient.Map(resp)[0]
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
	const pageSize = 50
	first, err := qfClient.GetValue(ctx, 1, pageSize)
	if err != nil {
		log.Error().Err(err).Int("page", 1).Msg("Failed to get value")
		return false
	}

	expected := first.Data.Total
	pages := (expected + pageSize - 1) / pageSize
	log.Info().Int("pages", pages).Int("total", expected).Msg("Fetched first page")
	records := make([]map[string]string, 0, expected)
	numbers := make(map[string]struct{}, expected)
	for page := 1; page <= pages; page++ {
		resp := first
		if page > 1 {
			resp, err = qfClient.GetValue(ctx, page, pageSize)
			if err != nil {
				log.Error().Err(err).Int("page", page).Msg("Failed to get value")
				return false
			}
		}
		for _, item := range qfClient.Map(resp) {
			number := strings.TrimSpace(item[IDFieldName])
			_, duplicate := numbers[number]
			if number == "" || duplicate {
				log.Error().Int("page", page).Bool("duplicate", duplicate).Msg("Invalid QingFlow record number")
				return false
			}
			numbers[number] = struct{}{}
			records = append(records, item)
		}
		log.Info().Int("page", page).Int("pages", pages).Int("fetched", len(records)).
			Int("expected", expected).Dur("elapsed", time.Since(started)).Msg("QingFlow fetch progress")
	}
	if len(records) != expected {
		log.Error().Int("fetched", len(records)).Int("expected", expected).Msg("QingFlow record count changed during pagination")
		return false
	}
	if expected == 0 {
		log.Info().Int("expected", 0).Bool("success", true).Msg("Sync summary")
		return true
	}

	index, err := larkClient.RecordIndex(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to index Feishu records")
		return false
	}
	log.Info().Int("indexed", len(index)).Dur("elapsed", time.Since(started)).Msg("Indexed Feishu records")

	var created, updated, failed int
	writeStarted := time.Now()
	for i, item := range records {
		number := strings.TrimSpace(item[IDFieldName])
		matches := index[number]
		if len(matches) > 1 {
			failed++
			log.Error().Int("record", i+1).Int("matches", len(matches)).Msg("Duplicate Feishu record number")
		} else {
			recordID := ""
			if len(matches) == 1 {
				recordID = matches[0]
			}
			wasCreated, err := larkClient.WriteRecord(ctx, recordID, item)
			switch {
			case err != nil:
				failed++
				log.Error().Err(err).Int("record", i+1).Msg("Failed to write record")
			case wasCreated:
				created++
			default:
				updated++
			}
		}
		if done := i + 1; done%25 == 0 || done == expected {
			log.Info().Int("processed", done).Int("expected", expected).
				Int("created", created).Int("updated", updated).Int("failed", failed).
				Dur("elapsed", time.Since(writeStarted)).Msg("Feishu write progress")
		}
	}

	verified, missing, duplicates := 0, 0, 0
	index, err = larkClient.RecordIndex(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to verify Feishu records")
	} else {
		for number := range numbers {
			switch len(index[number]) {
			case 0:
				missing++
			case 1:
				verified++
			default:
				duplicates++
			}
		}
	}
	success := failed == 0 && verified == expected
	log.Info().Int("expected", expected).Int("created", created).Int("updated", updated).
		Int("failed", failed).Int("verified", verified).Int("missing", missing).
		Int("duplicates", duplicates).Bool("readback_ok", err == nil).
		Dur("elapsed", time.Since(started)).Bool("success", success).Msg("Sync summary")
	return success
}
