package main

import (
	"context"
	"encoding/json"
	"os"

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
	const pageSize = 50
	first, err := qfClient.GetValue(ctx, 1, pageSize)
	if err != nil {
		log.Error().Err(err).Int("page", 1).Msg("Failed to get value")
		return false
	}

	expected := first.Data.Total
	pages := (expected + pageSize - 1) / pageSize
	log.Info().Int("pages", pages).Int("total", expected).Msg("Fetched first page")
	var created, updated, failed, failedPages int
	for page := 1; page <= pages; page++ {
		resp := first
		if page > 1 {
			resp, err = qfClient.GetValue(ctx, page, pageSize)
			if err != nil {
				failedPages++
				log.Error().Err(err).Int("page", page).Msg("Failed to get value")
				continue
			}
		}
		for _, item := range qfClient.Map(resp) {
			wasCreated, err := larkClient.WriteRecord(ctx, item)
			switch {
			case err != nil:
				failed++
				log.Error().Err(err).Int("page", page).Msg("Failed to write record")
			case wasCreated:
				created++
			default:
				updated++
			}
		}
	}

	success := failed == 0 && failedPages == 0 && created+updated == expected
	log.Info().Int("expected", expected).Int("created", created).Int("updated", updated).
		Int("failed", failed).Int("failed_pages", failedPages).Bool("success", success).Msg("Sync summary")
	return success
}
