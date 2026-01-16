package main

import (
	"context"
	"encoding/json"
	"math"
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

	log.Info().Msg("Starting job")

	ctx := context.Background()
	resp, err := qfClient.GetValue(ctx, 1, 50)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get value")
		os.Exit(1)
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

	var wg sync.WaitGroup
	pageNum := int(math.Ceil(float64(resp.Data.Total) / 50))
	wg.Add(pageNum)
	for i := 2; i <= pageNum; i++ {
		go func(i int) {
			defer wg.Done()

			resp, err := qfClient.GetValue(ctx, i, 50)
			if err != nil {
				log.Error().Err(err).Msg("Failed to get value")
				return
			}

			jobForResp(ctx, resp)
		}(i)
	}

	go func() {
		defer wg.Done()
		jobForResp(ctx, resp)
	}()

	wg.Wait()

	log.Info().Msg("Job done")
}
