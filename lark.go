package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	lark "github.com/larksuite/oapi-sdk-go/v3"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type LarkClient struct {
	*lark.Client
	AppToken                              string
	TableId                               string
	searchLimit, createLimit, updateLimit *rate.Limiter
}

func NewLarkClient(appId, appSecret, appToken, tableId string, options ...lark.ClientOptionFunc) *LarkClient {
	options = append([]lark.ClientOptionFunc{lark.WithHttpClient(feishuHTTPClient{&http.Client{Timeout: 20 * time.Second}})}, options...)
	return &LarkClient{
		Client:      lark.NewClient(appId, appSecret, options...),
		AppToken:    appToken,
		TableId:     tableId,
		searchLimit: rate.NewLimiter(20, 1),
		createLimit: rate.NewLimiter(50, 1),
		updateLimit: rate.NewLimiter(50, 1),
	}
}

const (
	IDFieldName         = "编号"
	UpdateTimeFieldName = "更新时间"
	CreateTimeFieldName = "申请时间"
)

func (c *LarkClient) CreateBatch(ctx context.Context, records []*larkbitable.AppTableRecord) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	token[6] = token[6]&0x0f | 0x40
	token[8] = token[8]&0x3f | 0x80
	clientToken := fmt.Sprintf("%x-%x-%x-%x-%x", token[:4], token[4:6], token[6:8], token[8:10], token[10:])
	req := larkbitable.NewBatchCreateAppTableRecordReqBuilder().
		AppToken(c.AppToken).TableId(c.TableId).ClientToken(clientToken).
		Body(larkbitable.NewBatchCreateAppTableRecordReqBodyBuilder().Records(records).Build()).Build()
	return retryFeishu(ctx, "batch_create", c.createLimit, func() error {
		resp, err := c.Bitable.AppTableRecord.BatchCreate(ctx, req)
		if err != nil {
			return err
		}
		if err := feishuResponseError(resp.ApiResp, resp.CodeError); err != nil {
			return err
		}
		if resp.Data == nil || len(resp.Data.Records) != len(records) {
			return fmt.Errorf("unexpected Feishu create response count, expected %d", len(records))
		}
		return nil
	})
}

func (c *LarkClient) UpdateBatch(ctx context.Context, records []*larkbitable.AppTableRecord) error {
	req := larkbitable.NewBatchUpdateAppTableRecordReqBuilder().
		AppToken(c.AppToken).TableId(c.TableId).
		Body(larkbitable.NewBatchUpdateAppTableRecordReqBodyBuilder().Records(records).Build()).Build()
	return retryFeishu(ctx, "batch_update", c.updateLimit, func() error {
		resp, err := c.Bitable.AppTableRecord.BatchUpdate(ctx, req)
		if err != nil {
			return err
		}
		if err := feishuResponseError(resp.ApiResp, resp.CodeError); err != nil {
			return err
		}
		if resp.Data == nil || len(resp.Data.Records) != len(records) {
			return fmt.Errorf("unexpected Feishu update response count, expected %d", len(records))
		}
		return nil
	})
}

// Keep the cooldown header even when an HTTP 429 response has no JSON body.
type feishuHTTPClient struct{ *http.Client }

func (c feishuHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.Client.Do(req)
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		defer resp.Body.Close()
		return nil, &feishuError{status: resp.StatusCode, retryAfter: resp.Header.Get("x-ogw-ratelimit-reset")}
	}
	return resp, err
}

type feishuError struct {
	status     int
	code       larkcore.CodeError
	retryAfter string
}

func (e *feishuError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, e.code.Error())
}

func feishuResponseError(resp *larkcore.ApiResp, code larkcore.CodeError) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && code.Code == 0 {
		return nil
	}
	return &feishuError{status: resp.StatusCode, code: code, retryAfter: resp.Header.Get("x-ogw-ratelimit-reset")}
}

func retryFeishu(ctx context.Context, operation string, limiter *rate.Limiter, call func() error) error {
	const maxAttempts = 5
	for attempt := 1; ; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		err := call()
		if err == nil {
			return nil
		}

		var apiError *feishuError
		if attempt == maxAttempts || !errors.As(err, &apiError) {
			return fmt.Errorf("feishu %s after %d attempt(s): %w", operation, attempt, err)
		}
		delay, retry := feishuRetryDelay(apiError, attempt)
		if !retry {
			return fmt.Errorf("feishu %s after %d attempt(s): %w", operation, attempt, err)
		}
		log.Warn().Str("operation", operation).Int("http_status", apiError.status).
			Int("code", apiError.code.Code).Int("attempt", attempt).Dur("backoff", delay).Msg("Retrying Feishu request")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func feishuRetryDelay(err *feishuError, attempt int) (time.Duration, bool) {
	switch err.code.Code {
	case 99991400, 1254290, 1254607, 1254291:
	default:
		if err.status != http.StatusTooManyRequests {
			return 0, false
		}
	}
	if seconds, parseErr := strconv.ParseInt(strings.TrimSpace(err.retryAfter), 10, 32); parseErr == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, true
	}
	return time.Second << (attempt - 1), true
}

var tzLocation = time.FixedZone("CST", 8*60*60)

func parseFields(fields map[string]string) map[string]interface{} {
	return lo.MapEntries(fields, func(k string, v string) (string, interface{}) {
		k = strings.TrimSpace(k)
		if k == UpdateTimeFieldName || k == CreateTimeFieldName {
			// 2024-11-24 20:15:35
			t, err := time.ParseInLocation("2006-01-02 15:04:05", v, tzLocation)
			if err != nil {
				log.Error().Err(err).Str("time", v).Msg("Failed to parse time")
				t = time.Now()
			}

			return k, t.UnixMilli()
		} else if k == "URL" {
			return k, map[string]interface{}{
				"link": v,
			}
		}

		return k, v
	})
}

func (c *LarkClient) RecordIndex(ctx context.Context) (map[string][]string, error) {
	index := make(map[string][]string)
	tokens := make(map[string]bool)
	token := ""
	for {
		req := larkbitable.NewSearchAppTableRecordReqBuilder().
			AppToken(c.AppToken).
			TableId(c.TableId).
			PageSize(500).
			Body(larkbitable.NewSearchAppTableRecordReqBodyBuilder().
				FieldNames([]string{IDFieldName}).
				AutomaticFields(false).
				Build())
		if token != "" {
			req.PageToken(token)
		}
		var data *larkbitable.SearchAppTableRecordRespData
		if err := retryFeishu(ctx, "search", c.searchLimit, func() error {
			resp, err := c.Bitable.AppTableRecord.Search(ctx, req.Build())
			if err != nil {
				return err
			}
			if err := feishuResponseError(resp.ApiResp, resp.CodeError); err != nil {
				return err
			}
			data = resp.Data
			return nil
		}); err != nil {
			return nil, err
		}
		if data == nil {
			return nil, errors.New("Feishu search returned no data")
		}

		for _, item := range data.Items {
			if item == nil || item.RecordId == nil || *item.RecordId == "" {
				return nil, errors.New("Feishu search returned a record without an ID")
			}
			number, err := feishuRecordNumber(item.Fields[IDFieldName])
			if err != nil {
				return nil, err
			}
			if number != "" {
				index[number] = append(index[number], *item.RecordId)
			}
		}
		if data.HasMore == nil || !*data.HasMore {
			log.Info().Int("pages", len(tokens)+1).Msg("Fetched Feishu index")
			return index, nil
		}
		if data.PageToken == nil || *data.PageToken == "" || tokens[*data.PageToken] {
			return nil, errors.New("Feishu search returned an invalid pagination token")
		}
		token = *data.PageToken
		tokens[token] = true
	}
}

func feishuRecordNumber(value interface{}) (string, error) {
	switch value := value.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(value), nil
	case []interface{}:
		var number strings.Builder
		for _, part := range value {
			field, ok := part.(map[string]interface{})
			if !ok {
				return "", errors.New("unexpected Feishu record number segment")
			}
			text, ok := field["text"].(string)
			if !ok {
				return "", errors.New("Feishu record number segment has no text")
			}
			number.WriteString(text)
		}
		return strings.TrimSpace(number.String()), nil
	default:
		return "", fmt.Errorf("unexpected Feishu record number type %T", value)
	}
}
