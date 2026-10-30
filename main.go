package main

import (
	"context"
	"encoding/json"
	"os"
	"sync"

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
	defer qfClient.Close()

	if dryRun {
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

	const pageSize = 50

	log.Info().Msg("Starting job")

	ctx := context.Background()
	resp, err := qfClient.GetValue(ctx, 1, pageSize)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get value")
		os.Exit(1)
		return
	}

	total := resp.Data.Total
	if total == 0 {
		log.Info().Msg("No records to sync")
		return
	}

	jobForResp := func(ctx context.Context, resp *FilterResponse) {
		m := qfClient.Map(resp)

		var wg sync.WaitGroup
		wg.Add(len(m))
		for _, item := range m {
			go func(item map[string]string) {
				defer wg.Done()

				err := larkClient.WriteRecord(ctx, item)
				if err != nil {
					log.Error().Err(err).Msg("Failed to write record")
				}
			}(item)
		}

		wg.Wait()
	}

	pageNum := (total + pageSize - 1) / pageSize
	log.Info().Int("total", total).Int("pages", pageNum).Msg("Fetched first page")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		jobForResp(ctx, resp)
	}()

	for i := 2; i <= pageNum; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			page, err := qfClient.GetValue(ctx, i, pageSize)
			if err != nil {
				log.Error().Err(err).Int("page", i).Msg("Failed to get value")
				return
			}

			jobForResp(ctx, page)
		}(i)
	}

	wg.Wait()

	log.Info().Msg("Job done")
}
