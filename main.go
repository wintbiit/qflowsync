package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const JobTimeout = 5 * time.Minute

var dryRun = os.Getenv("DRY_RUN") == "true"

func main() {
	config := MustLoadConfig()
	interval, err := time.ParseDuration(config.Interval)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse interval")
	}

	larkClient := NewLarkClient(config.Lark.AppId, config.Lark.AppSecret, config.Lark.AppToken, config.Lark.TableId)

	qfClinet, err := NewQFlowClient(config.QFlow.AppId, config.QFlow.ViewId)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create QingFlow client")
	}
	defer qfClinet.Close()

	if dryRun {
		log.Warn().Msg("Dry run mode")
		resp, err := qfClinet.GetValue(context.Background(), 1, 1)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get value")
		}

		m := qfClinet.Map(resp)[0]
		j, _ := json.MarshalIndent(m, "", "  ")
		log.Info().Msgf("Dry run data: \n%s", j)
		return
	}

	job := func(ctx context.Context) {
		log.Info().Msg("Starting job")

		resp, err := qfClinet.GetValue(ctx, 1, 50)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get value")
			return
		}

		jobForResp := func(ctx context.Context, resp *FilterResponse) {
			m := qfClinet.Map(resp)

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

				resp, err := qfClinet.GetValue(ctx, i, 50)
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

	job(context.Background())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), JobTimeout)
		job(ctx)
		cancel()
	}
}
