package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
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
	AppToken string
	TableId  string
}

func NewLarkClient(appId, appSecret, appToken, tableId string) *LarkClient {
	return &LarkClient{
		Client:   lark.NewClient(appId, appSecret, lark.WithReqTimeout(20*time.Second)),
		AppToken: appToken,
		TableId:  tableId,
	}
}

const (
	IDFieldName         = "编号"
	UpdateTimeFieldName = "更新时间"
	CreateTimeFieldName = "申请时间"
)

func (c *LarkClient) WriteRecord(ctx context.Context, recordID string, rec map[string]string) (bool, error) {
	fields := parseFields(rec)
	if recordID != "" {
		return false, retryLark(ctx, "update", func() error {
			return c.UpdateRecord(ctx, recordID, fields)
		})
	}

	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return false, err
	}
	token[6] = token[6]&0x0f | 0x40
	token[8] = token[8]&0x3f | 0x80
	clientToken := fmt.Sprintf("%x-%x-%x-%x-%x", token[:4], token[4:6], token[6:8], token[8:10], token[10:])
	err := retryLark(ctx, "create", func() error {
		_, err := c.CreateRecord(ctx, fields, clientToken)
		return err
	})
	return err == nil, err
}

func retryLark(ctx context.Context, operation string, call func() error) error {
	const maxAttempts = 5
	delay := time.Second
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := call()
		if err == nil {
			return nil
		}
		var apiError larkcore.CodeError
		if attempt == maxAttempts || !errors.As(err, &apiError) ||
			(apiError.Code != 1254290 && apiError.Code != 1254607) {
			return fmt.Errorf("feishu %s after %d attempt(s): %w", operation, attempt, err)
		}

		log.Warn().Str("operation", operation).Int("code", apiError.Code).
			Int("attempt", attempt).Dur("backoff", delay).Msg("Retrying Feishu request")
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
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
		if err := retryLark(ctx, "search", func() error {
			resp, err := c.Bitable.AppTableRecord.Search(ctx, req.Build())
			if err != nil {
				return err
			}
			if !resp.Success() {
				return resp.CodeError
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

// Pace creates and updates through the same limiter.
var writeRecordLimiter = rate.NewLimiter(5, 1)

func (c *LarkClient) UpdateRecord(ctx context.Context, id string, fields map[string]interface{}) error {
	if err := writeRecordLimiter.Wait(ctx); err != nil {
		return err
	}

	req := larkbitable.NewUpdateAppTableRecordReqBuilder().
		AppToken(c.AppToken).
		TableId(c.TableId).
		RecordId(id).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			RecordId(id).
			Fields(fields).
			Build()).
		Build()

	// 发起请求
	resp, err := c.Bitable.AppTableRecord.Update(ctx, req)
	if err != nil {
		return err
	}

	if !resp.Success() {
		return resp.CodeError
	}

	return nil
}

func (c *LarkClient) CreateRecord(ctx context.Context, fields map[string]interface{}, clientToken string) (string, error) {
	if err := writeRecordLimiter.Wait(ctx); err != nil {
		return "", err
	}

	req := larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(c.AppToken).
		TableId(c.TableId).
		ClientToken(clientToken).
		AppTableRecord(larkbitable.NewAppTableRecordBuilder().
			Fields(fields).
			Build()).
		Build()

	// 发起请求
	resp, err := c.Bitable.AppTableRecord.Create(ctx, req)
	if err != nil {
		return "", err
	}

	if !resp.Success() {
		return "", resp.CodeError
	}

	return *resp.Data.Record.RecordId, nil
}
