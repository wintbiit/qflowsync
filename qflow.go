package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
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

func validCookieValue(v string) bool {
	for i := 0; i < len(v); i++ {
		b := v[i]
		// RFC 6265 cookie-octet, same as net/http.validCookieValueByte
		if b < 0x20 || b >= 0x7f || b == '"' || b == ';' || b == '\\' {
			return false
		}
	}
	return true
}

func parseCookies(cookies string) []*http.Cookie {
	var out []*http.Cookie
	for _, part := range strings.Split(cookies, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		name, value, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" {
			log.Warn().Str("cookie", part).Msg("Skipping malformed cookie")
			continue
		}
		if !validCookieValue(value) {
			log.Warn().Str("name", name).Msg("Skipping cookie with invalid characters in value")
			continue
		}

		out = append(out, &http.Cookie{Name: name, Value: value})
	}
	return out
}

func NewQFlowClient(appId, viewId, cookies string) (*QFlowClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cookie jar")
	}

	jar.SetCookies(&qfUrl, parseCookies(cookies))

	client := http.Client{Jar: jar, Timeout: 20 * time.Second}

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

		if item.ApplyBaseInfo.ApplyTime != "" {
			m[CreateTimeFieldName] = item.ApplyBaseInfo.ApplyTime
		}
		if item.LastUpdateTime > 0 {
			m[UpdateTimeFieldName] = time.UnixMilli(item.LastUpdateTime).Format(time.RFC3339Nano)
		} else if item.ApplyBaseInfo.LastUpdateTime != "" {
			m[UpdateTimeFieldName] = item.ApplyBaseInfo.LastUpdateTime
		}
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

var qflowGetValueLimiter = rate.NewLimiter(10, 1)

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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.Errorf("qingflow http status %d", resp.StatusCode)
	}

	var filterResp FilterResponse
	err = json.NewDecoder(resp.Body).Decode(&filterResp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}

	if filterResp.Code != 0 && filterResp.Code != 200 {
		return nil, errors.Errorf("qingflow api code=%d message=%s", filterResp.Code, filterResp.Message)
	}

	return &filterResp, nil
}

// AllRecords fetches independent pages concurrently and assembles them in page order.
func (c *QFlowClient) AllRecords(ctx context.Context) ([]map[string]string, error) {
	const pageSize, concurrentPages = 50, 10
	first, err := c.GetValue(ctx, 1, pageSize)
	if err != nil {
		return nil, fmt.Errorf("page 1: %w", err)
	}
	expected := first.Data.Total
	if expected < 0 {
		return nil, fmt.Errorf("invalid QingFlow record total %d", expected)
	}
	pageCount := 1
	if expected > 0 {
		pageCount = (expected-1)/pageSize + 1
	}
	pages := make([]*FilterResponse, pageCount)
	pages[0] = first

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var fetchErr error
	var fail sync.Once
	for worker := 0; worker < concurrentPages && worker < pageCount-1; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for page := worker + 2; page <= pageCount; page += concurrentPages {
				if ctx.Err() != nil {
					return
				}
				resp, err := c.GetValue(ctx, page, pageSize)
				if err != nil {
					fail.Do(func() {
						fetchErr = fmt.Errorf("page %d: %w", page, err)
						cancel()
					})
					return
				}
				pages[page-1] = resp
			}
		}(worker)
	}
	workers.Wait()
	if fetchErr != nil {
		return nil, fetchErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	records := make([]map[string]string, 0, expected)
	for i, page := range pages {
		want := pageSize
		if remaining := expected - i*pageSize; remaining < want {
			want = remaining
		}
		if page.Data.Total != expected || len(page.Data.List) != want {
			return nil, fmt.Errorf("QingFlow pagination changed on page %d: total %d/%d, rows %d/%d",
				i+1, page.Data.Total, expected, len(page.Data.List), want)
		}
		records = append(records, c.Map(page)...)
	}
	log.Info().Int("pages", pageCount).Int("records", len(records)).Msg("Fetched QingFlow")
	return records, nil
}
