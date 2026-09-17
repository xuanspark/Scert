package dnsprovider

import "testing"

func TestAliyunAuthorizationRecordDNSCaseAndProof(t *testing.T) {
	base := aliyunRecordInfo{RecordID: "record-1", DomainName: "example.com", RR: "_scert-authorization-mixed.web", Type: "TXT", Value: "ExactProofAa"}
	tests := []struct {
		name   string
		change func(*aliyunRecordInfo)
		want   bool
	}{
		{"DNS名称大小写归一化", func(info *aliyunRecordInfo) {}, true},
		{"不同记录ID拒绝", func(info *aliyunRecordInfo) { info.RecordID = "record-2" }, false},
		{"不同域名拒绝", func(info *aliyunRecordInfo) { info.DomainName = "other.com" }, false},
		{"不同主机记录拒绝", func(info *aliyunRecordInfo) { info.RR += ".other" }, false},
		{"不同类型拒绝", func(info *aliyunRecordInfo) { info.Type = "CNAME" }, false},
		{"TXT值大小写不可忽略", func(info *aliyunRecordInfo) { info.Value = "exactproofaa" }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := base
			test.change(&info)
			if got := matchesAliyunAuthorizationRecord(info, "record-1", "example.com", "_scert-authorization-MiXeD.web", "ExactProofAa"); got != test.want {
				t.Fatalf("记录比对结果错误：got=%v want=%v", got, test.want)
			}
		})
	}
}
