package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
)

type qingFlowTestTransport func(*http.Request) (*http.Response, error)

func (f qingFlowTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFullSync(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		badCreateCount bool
		want           []string
	}{
		{"success", false, []string{"field:URL", "create:500", "create:1", "update:1"}},
		{"failure", true, []string{"field:URL", "create:500"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			const total = 502
			qf := &QFlowClient{Client: &http.Client{Transport: qingFlowTestTransport(func(req *http.Request) (*http.Response, error) {
				var input FilterRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					return nil, err
				}
				start := (input.Filter.PageNum-1)*50 + 1
				var rows []map[string]interface{}
				for id := start; id < start+50 && id <= total; id++ {
					rows = append(rows, map[string]interface{}{
						"applyId": id,
						"answers": []interface{}{map[string]interface{}{
							"queTitle": IDFieldName, "values": []interface{}{map[string]string{"value": strconv.Itoa(id)}},
						}},
					})
				}
				body, err := json.Marshal(map[string]interface{}{"code": 0, "data": map[string]interface{}{"total": total, "list": rows}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, err
			})}}

			calls := make(chan string, 8)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					fmt.Fprint(w, `{"code":0,"tenant_access_token":"test","expire":7200}`)
				case strings.HasSuffix(r.URL.Path, "/fields") && r.Method == http.MethodGet:
					fmt.Fprint(w, `{"code":0,"data":{"has_more":false,"items":[{"field_name":"编号","type":1}]}}`)
				case strings.HasSuffix(r.URL.Path, "/fields") && r.Method == http.MethodPost:
					var field larkbitable.AppTableField
					if err := json.NewDecoder(r.Body).Decode(&field); err != nil || field.FieldName == nil || field.Type == nil {
						t.Error("invalid create-field request")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if *field.Type != 15 || r.URL.Query().Get("client_token") == "" {
						t.Error("expected an idempotent link-column request")
					}
					calls <- "field:" + *field.FieldName
					fmt.Fprint(w, `{"code":0,"data":{"field":{"field_name":"URL","type":15}}}`)
				case strings.HasSuffix(r.URL.Path, "/search"):
					fmt.Fprint(w, `{"code":0,"data":{"has_more":false,"items":[{"record_id":"first","fields":{"编号":"1"}}]}}`)
				default:
					var body struct{ Records []*larkbitable.AppTableRecord }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/batch_create") {
						calls <- fmt.Sprintf("create:%d", len(body.Records))
						if r.URL.Query().Get("client_token") == "" {
							t.Error("missing create idempotency token")
						}
						if scenario.badCreateCount {
							body.Records = nil
						}
					} else if strings.HasSuffix(r.URL.Path, "/batch_update") {
						calls <- fmt.Sprintf("update:%d", len(body.Records))
						if len(body.Records) != 1 || body.Records[0].RecordId == nil || *body.Records[0].RecordId != "first" {
							t.Error("incorrect update record")
						}
					} else {
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": body})
				}
			})
			server := httptest.NewServer(handler)
			defer server.Close()
			dest := NewLarkClient("test-"+scenario.name, "secret", "app", "table", lark.WithOpenBaseUrl(server.URL))
			if success := syncRecords(context.Background(), qf, dest); success == scenario.badCreateCount {
				t.Fatalf("sync success = %t", success)
			}
			close(calls)
			var got []string
			for call := range calls {
				got = append(got, call)
			}
			if !reflect.DeepEqual(got, scenario.want) {
				t.Fatalf("writes %v, want %v", got, scenario.want)
			}
		})
	}
}

func TestCreateRetryKeepsRequest(t *testing.T) {
	type attempt struct {
		token, body string
		at          time.Time
	}
	writes := make(chan attempt, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"test","expire":7200}`)
			return
		}
		retry := len(writes) == 0
		body, _ := io.ReadAll(r.Body)
		writes <- attempt{r.URL.Query().Get("client_token"), string(body), time.Now()}
		if retry {
			w.Header().Set("x-ogw-ratelimit-reset", "2")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"data":{"records":[{"record_id":"new"}]}}`)
	}))
	defer server.Close()

	dest := NewLarkClient("test-retry", "secret", "app", "table", lark.WithOpenBaseUrl(server.URL))
	record := larkbitable.NewAppTableRecordBuilder().Fields(map[string]interface{}{IDFieldName: "1"}).Build()
	if err := dest.CreateBatch(context.Background(), []*larkbitable.AppTableRecord{record}); err != nil {
		t.Fatal(err)
	}
	first, second := <-writes, <-writes
	if first.token == "" || first.token != second.token || first.body != second.body {
		t.Fatal("create retry changed the request")
	}
	if elapsed := second.at.Sub(first.at); elapsed < 2*time.Second {
		t.Fatalf("retry ignored cooldown: %s", elapsed)
	}
}

func TestFeishuRetryPolicy(t *testing.T) {
	for _, code := range []int{99991400, 1254290, 1254607, 1254291} {
		for attempt := 1; attempt <= 4; attempt++ {
			got, ok := feishuRetryDelay(&feishuError{code: larkcore.CodeError{Code: code}}, attempt)
			if !ok || got != time.Second<<(attempt-1) {
				t.Fatalf("code %d, attempt %d: %s, %t", code, attempt, got, ok)
			}
		}
	}
	if _, ok := feishuRetryDelay(&feishuError{code: larkcore.CodeError{Code: 1254015}}, 1); ok {
		t.Fatal("field type error should fail immediately")
	}
	if done, failed := writeBatches(context.Background(), "empty", nil, nil); done != 0 || failed != 0 {
		t.Fatal("empty batch should be skipped")
	}
}

func TestSyncRecordTimes(t *testing.T) {
	var source FilterResponseDataItem
	if err := json.Unmarshal([]byte(`{
		"applyBaseInfo":{"applyTime":"2026-11-16T16:33:20","lastUpdateTime":"2026-11-16T16:33:19","currentAuditNodes":[{"auditNodeName":"已通过"}]},
		"lastUpdateTime":1794818000123
	}`), &source); err != nil {
		t.Fatal(err)
	}
	qf := &QFlowClient{}
	mapped := qf.Map(&FilterResponse{Data: FilterResponseData{List: []FilterResponseDataItem{source}}})
	fields, err := parseFields(mapped[0])
	if err != nil {
		t.Fatal(err)
	}
	if fields[WorkflowFieldName] != "已通过" {
		t.Fatal("missing workflow node name")
	}
	if fields[CreateTimeFieldName] != int64(1794818000000) || fields[UpdateTimeFieldName] != int64(1794818000123) {
		t.Fatalf("incorrect timestamp conversion: created=%v updated=%v", fields[CreateTimeFieldName], fields[UpdateTimeFieldName])
	}
	if _, err := parseFields(map[string]string{CreateTimeFieldName: "invalid"}); err == nil {
		t.Fatal("invalid timestamp should fail instead of writing the current time")
	}
}
