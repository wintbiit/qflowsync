package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"golang.org/x/time/rate"
)

var qfUrl = url.URL{Scheme: "https", Host: "qingflow.com"}

type QFlowClient struct {
	*http.Client
	appId  string
	viewId string
}

func NewQFlowClient(appId, viewId string) (*QFlowClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cookie jar")
	}

	if _, err = os.Stat("cookie.txt"); err == nil {
		f, err := os.Open("cookie.txt")
		if err != nil {
			return nil, errors.Wrap(err, "failed to open cookie file")
		}

		content, err := io.ReadAll(f)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read cookie file")
		}

		cookies := lo.Map(strings.Split(string(content), ";"), func(s string, i int) *http.Cookie {
			pair := strings.TrimSpace(s)
			sp := strings.SplitN(pair, "=", 2)
			if len(sp) != 2 {
				return &http.Cookie{
					Name:  pair,
					Value: "",
				}
			}
			return &http.Cookie{
				Name:  sp[0],
				Value: sp[1],
			}
		})

		jar.SetCookies(&qfUrl, cookies)
		log.Info().Msg("successfully loaded cookies from file")
	}

	client := http.Client{Jar: jar}

	return &QFlowClient{
		appId:  appId,
		viewId: viewId,
		Client: &client,
	}, nil
}

var blacklistKeywords = []string{
	"附件",
	"图片",
}

func (c *QFlowClient) Map(r *FilterResponse) []map[string]string {
	return lo.Map(r.Data.List, func(item FilterResponseDataItem, i int) map[string]string {
		answers := lo.Filter(item.Answers, func(answer FilterResponseDataItemAnswer, index int) bool {
			for _, keyword := range blacklistKeywords {
				if strings.Contains(answer.QueTitle, keyword) {
					return false
				}
			}

			return true
		})

		m := lo.SliceToMap(answers, func(answer FilterResponseDataItemAnswer) (string, string) {
			if len(answer.Values) == 0 {
				return answer.QueTitle, ""
			}

			return answer.QueTitle, answer.Values[0].Value
		})

		m["URL"] = fmt.Sprintf("https://qingflow.com/appView/%s/shareView/%s?applyId=%d", c.appId, c.viewId, item.ApplyId)

		return m
	})
}

func (c *QFlowClient) Close() error {
	cookies := c.Jar.Cookies(&qfUrl)
	cookieStrings := lo.Map(cookies, func(c *http.Cookie, i int) string {
		return c.String()
	})

	f, err := os.OpenFile("cookie.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.Wrap(err, "failed to create cookie file")
	}

	_, err = f.WriteString(strings.Join(cookieStrings, ";"))
	if err != nil {
		return errors.Wrap(err, "failed to write cookie file")
	}

	return nil
}

var qflowGetValueLimiter = rate.NewLimiter(rate.Every(1*time.Second), 1)

func (c *QFlowClient) GetValue(ctx context.Context, page int, size int) (*FilterResponse, error) {
	if err := qflowGetValueLimiter.Wait(ctx); err != nil {
		return nil, err
	}

	uri := fmt.Sprintf("https://qingflow.com/api/view/%s/apply/filter", c.viewId)
	filter := FilterRequest{
		Filter: Filter{
			PageSize: size,
			PageNum:  page,
			Type:     8,
			QueryKey: nil,
			Queries:  []interface{}{},
			Sorts:    []FilterSort{updateTimeSorter},
		},
	}

	marshaled, err := json.Marshal(filter)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal filter")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", uri, strings.NewReader(string(marshaled)))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}

	defer resp.Body.Close()

	var filterResp FilterResponse
	err = json.NewDecoder(resp.Body).Decode(&filterResp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	return &filterResp, nil
}
