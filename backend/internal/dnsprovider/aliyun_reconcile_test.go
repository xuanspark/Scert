package dnsprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/go-acme/alidns-20150109/v5/client"
)

func TestAliyunAuthorizationLostAddResponse(t *testing.T) {
	for _, scenario := range []string{"cleanup", "wrong_value", "wrong_zone", "wrong_rr", "wrong_type", "empty_id", "multiple", "missing", "incomplete", "query_error", "delete_error"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var actions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				action := r.Header.Get("x-acs-action")
				mu.Lock()
				actions = append(actions, action)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch action {
				case "AddDomainRecord":
					// 模拟云端已经接受写入，但创建响应在传输中丢失。
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
				case "DescribeDomainRecords":
					for key, expected := range map[string]string{
						"DomainName": "example.com", "RRKeyWord": "_scert-authorization-unique.web",
						"TypeKeyWord": "TXT", "ValueKeyWord": "unique-proof", "SearchMode": "COMBINATION",
					} {
						if r.Form.Get(key) != expected {
							t.Errorf("补偿查询未限定本次记录：%s", key)
						}
					}
					if scenario == "query_error" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"Code":"Forbidden.RAM","Message":"denied"}`))
						return
					}
					record := map[string]string{"RecordId": "owned-id", "DomainName": "example.com", "RR": "_scert-authorization-unique.web", "Type": "TXT", "Value": "unique-proof"}
					switch scenario {
					case "wrong_value":
						record["Value"] = "other-proof"
					case "wrong_zone":
						record["DomainName"] = "other.com"
					case "wrong_rr":
						record["RR"] = "other"
					case "wrong_type":
						record["Type"] = "A"
					case "empty_id":
						record["RecordId"] = ""
					}
					records := []map[string]string{record}
					if scenario == "multiple" {
						records = append(records, record)
					} else if scenario == "missing" {
						records = nil
					}
					total := len(records)
					if scenario == "incomplete" {
						total++
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"TotalCount": total, "DomainRecords": map[string]any{"Record": records}})
				case "DeleteDomainRecord":
					if r.Form.Get("RecordId") != "owned-id" {
						t.Error("不得删除其他记录")
					}
					if scenario == "delete_error" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"Code":"Forbidden.RAM","Message":"denied"}`))
						return
					}
					_, _ = w.Write([]byte(`{"RecordId":"owned-id"}`))
				default:
					t.Errorf("不允许的云端操作：%s", action)
				}
			}))
			defer server.Close()
			sdk, err := client.NewClient(new(openapi.Config).SetAccessKeyId("test-id").SetAccessKeySecret("test-secret").
				SetEndpoint(strings.TrimPrefix(server.URL, "http://")).SetProtocol("http").
				SetRetryOptions(&dara.RetryOptions{Retryable: false, MaxAttempts: 1}))
			if err != nil {
				t.Fatal(err)
			}
			id, err := (aliyunRecords{client: sdk}).Add(t.Context(), "example.com", "_scert-authorization-unique.web", "unique-proof")
			if err == nil || id != "" {
				t.Fatal("创建结果不确定不能变成验证成功")
			}
			if scenario != "cleanup" && !strings.Contains(err.Error(), "需要核对") {
				t.Fatalf("补偿未完成必须保留人工核对信息：%v", err)
			}
			if scenario != "cleanup" && !strings.Contains(DiagnoseAliyunError(err, "example.com"), "_scert-authorization-unique.web.example.com") {
				t.Fatal("页面诊断必须保留待核对记录，不能只显示通用超时")
			}
			mu.Lock()
			defer mu.Unlock()
			want := "AddDomainRecord,DescribeDomainRecords"
			if scenario == "cleanup" || scenario == "delete_error" {
				want += ",DeleteDomainRecord"
			}
			if strings.Join(actions, ",") != want {
				t.Fatalf("云端操作越界或自动重试创建：%v", actions)
			}
		})
	}
}

func TestAliyunReconcilePreservesCancellationAndExclusions(t *testing.T) {
	api := aliyunRecords{}
	denied := errors.New("Forbidden.RAM")
	if got := api.reconcileAuthorizationAdd(t.Context(), "example.com", "_scert-authorization-unique", "proof", denied); got != denied {
		t.Fatal("明确拒绝不应触发补偿查询")
	}
	if got := api.reconcileAuthorizationAdd(t.Context(), "example.com", "_acme-challenge", "proof", context.DeadlineExceeded); got != context.DeadlineExceeded {
		t.Fatal("不得通过授权探测补偿删除共享 ACME 记录")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"TotalCount":0,"DomainRecords":{"Record":[]}}`))
	}))
	defer server.Close()
	sdk, err := client.NewClient(new(openapi.Config).SetAccessKeyId("test-id").SetAccessKeySecret("test-secret").SetEndpoint(strings.TrimPrefix(server.URL, "http://")).SetProtocol("http"))
	if err != nil {
		t.Fatal(err)
	}
	api.client = sdk
	started := time.Now()
	err = api.reconcileAuthorizationAdd(ctx, "example.com", "_scert-authorization-unique", "proof", context.Canceled)
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(started) > aliyunReconciliationTimeout {
		t.Fatalf("补偿应独立于取消且保持时限和原始错误：calls=%d err=%v", calls, err)
	}
}
