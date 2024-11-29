package main

import (
	"context"
	"strings"
	"time"

	"golang.org/x/time/rate"

	lark "github.com/larksuite/oapi-sdk-go/v3"
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
		Client:   lark.NewClient(appId, appSecret),
		AppToken: appToken,
		TableId:  tableId,
	}
}

const (
	IDFieldName         = "编号"
	UpdateTimeFieldName = "更新时间"
	CreateTimeFieldName = "申请时间"
)

func (c *LarkClient) WriteRecord(ctx context.Context, rec map[string]string) error {
	id, ok := rec[IDFieldName]
	if !ok {
		return nil
	}

	exists, recordId, err := c.RecordExists(ctx, id)
	if err != nil {
		return err
	}

	fields := parseFields(rec)

	if exists {
		return c.UpdateRecord(ctx, recordId, fields)
	}

	_, err = c.CreateRecord(ctx, fields)
	return err
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

// 20 times per second
var recordExistsLimiter = rate.NewLimiter(rate.Every(time.Second/15), 1)

func (c *LarkClient) RecordExists(ctx context.Context, id string) (bool, string, error) {
	if err := recordExistsLimiter.Wait(ctx); err != nil {
		return false, "", err
	}

	req := larkbitable.NewSearchAppTableRecordReqBuilder().
		AppToken(c.AppToken).
		TableId(c.TableId).
		PageSize(1).
		Body(larkbitable.NewSearchAppTableRecordReqBodyBuilder().
			FieldNames([]string{IDFieldName}).
			Filter(larkbitable.NewFilterInfoBuilder().
				Conjunction("and").
				Conditions([]*larkbitable.Condition{
					larkbitable.NewConditionBuilder().
						FieldName(IDFieldName).
						Operator("is").Value([]string{id}).
						Build(),
				}).Build()).
			AutomaticFields(false).
			Build()).
		Build()

	// 发起请求
	resp, err := c.Bitable.AppTableRecord.Search(ctx, req)
	if err != nil {
		return false, "", err
	}

	if !resp.Success() {
		return false, "", resp
	}

	if len(resp.Data.Items) == 0 {
		return false, "", nil
	}

	return true, *resp.Data.Items[0].RecordId, nil
}

// 50 times per second
var updateRecordLimiter = rate.NewLimiter(rate.Every(time.Second/40), 1)

func (c *LarkClient) UpdateRecord(ctx context.Context, id string, fields map[string]interface{}) error {
	if err := updateRecordLimiter.Wait(ctx); err != nil {
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
		return resp
	}

	return nil
}

// 50 times per second
var createRecordLimiter = rate.NewLimiter(rate.Every(time.Second/40), 1)

func (c *LarkClient) CreateRecord(ctx context.Context, fields map[string]interface{}) (string, error) {
	if err := createRecordLimiter.Wait(ctx); err != nil {
		return "", err
	}

	req := larkbitable.NewCreateAppTableRecordReqBuilder().
		AppToken(c.AppToken).
		TableId(c.TableId).
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
		return "", resp
	}

	return *resp.Data.Record.RecordId, nil
}
