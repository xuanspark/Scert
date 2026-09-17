package dnsprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/go-acme/alidns-20150109/v5/client"
)

func TestAliyunRecordsDescribeUsesScopedExactQuery(t *testing.T) {
	for _, found := range []bool{true, false} {
		name := "记录存在"
		if !found {
			name = "拒绝其他记录"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if got := r.Header.Get("x-acs-action"); got != "DescribeDomainRecords" {
					t.Errorf("调用了非限定查询接口：%q", got)
				}
				for key, expected := range map[string]string{
					"DomainName": "example.com",
					"RRKeyWord": "_scert-authorization-mixed.web", "TypeKeyWord": "TXT",
					"SearchMode": "EXACT", "PageNumber": "1", "PageSize": "100",
				} {
					if got := r.Form.Get(key); got != expected {
						t.Errorf("查询范围错误：%s=%q，预期 %q", key, got, expected)
					}
				}
				records := []map[string]string{{"RecordId": "other-record", "DomainName": "example.com", "RR": "_scert-authorization-mixed.web", "Type": "TXT", "Value": "unrelated"}}
				if found {
					records = append(records, map[string]string{"RecordId": "record-1", "DomainName": "example.com", "RR": "_scert-authorization-mixed.web", "Type": "TXT", "Value": "ExactProofAa"})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"TotalCount": len(records), "PageNumber": 1, "PageSize": 100, "DomainRecords": map[string]any{"Record": records}})
			}))
			defer server.Close()
			sdk, err := client.NewClient(new(openapi.Config).SetAccessKeyId("test-id").SetAccessKeySecret("test-secret").SetEndpoint(strings.TrimPrefix(server.URL, "http://")).SetProtocol("http"))
			if err != nil {
				t.Fatal(err)
			}
			api := aliyunRecords{client: sdk}
			info, err := api.Describe(t.Context(), "example.com", "_scert-authorization-mixed.web", "record-1")
			if found {
				if err != nil || !matchesAliyunAuthorizationRecord(info, "record-1", "example.com", "_scert-authorization-MiXeD.web", "ExactProofAa") {
					t.Fatalf("限定范围查询未通过：%+v %v", info, err)
				}
			} else if err == nil {
				t.Fatal("不得使用其他记录证明本次验证成功")
			}
			if calls != 1 {
				t.Fatalf("应只执行一次限定查询，实际 %d 次", calls)
			}
			if _, err := api.Describe(t.Context(), "", "", "record-1"); err == nil || calls != 1 {
				t.Fatal("缺少查询范围必须在调用 API 前拒绝")
			}
		})
	}
}
