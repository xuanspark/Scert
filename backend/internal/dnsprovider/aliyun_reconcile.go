package dnsprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/alibabacloud-go/tea/dara"
	"github.com/go-acme/alidns-20150109/v5/client"
)

const aliyunReconciliationTimeout = 10 * time.Second

type aliyunReconciliationError struct {
	error
	recordName string
}

// 创建响应丢失不等于云端未写入；仅核对本次随机授权探测记录，不重试创建。
// ACME 固定主机记录可能被其他挑战共享，不能使用此补偿入口删除。
func (a aliyunRecords) reconcileAuthorizationAdd(ctx context.Context, zone, rr, value string, cause error) error {
	var networkError net.Error
	uncertain := errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, context.Canceled) ||
		errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF) || errors.As(cause, &networkError)
	if !uncertain || !strings.HasPrefix(rr, "_scert-authorization-") {
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), aliyunReconciliationTimeout)
	defer cancel()
	err := a.cleanupUncertainAuthorizationRecord(cleanupCtx, zone, rr, value)
	if err != nil {
		err = &aliyunReconciliationError{error: err, recordName: rr + "." + zone}
		return errors.Join(cause, fmt.Errorf("授权探测创建结果不确定，记录 %s.%s 需要核对：%w", rr, zone, err))
	}
	// 即使补偿成功，本次权限验证仍然失败，不能将网络异常转成已授权。
	return cause
}

func (a aliyunRecords) cleanupUncertainAuthorizationRecord(ctx context.Context, zone, rr, value string) error {
	response, err := client.DescribeDomainRecordsWithContext(ctx, a.client,
		new(client.DescribeDomainRecordsRequest).SetDomainName(zone).SetRRKeyWord(rr).
			SetTypeKeyWord("TXT").SetValueKeyWord(value).SetSearchMode("COMBINATION").
			SetPageNumber(1).SetPageSize(100), &dara.RuntimeOptions{})
	if err != nil {
		return err
	}
	if response == nil || response.Body == nil || response.Body.DomainRecords == nil || response.Body.TotalCount == nil {
		return errors.New("云端未返回完整的记录核对结果")
	}
	records := response.Body.DomainRecords.Record
	if *response.Body.TotalCount != int64(len(records)) || len(records) > 1 {
		return errors.New("核对结果不唯一或分页不完整，已拒绝删除")
	}
	if len(records) == 0 {
		return errors.New("暂未发现记录，不能排除云端延迟写入，请稍后核对")
	}
	record := records[0]
	if record == nil {
		return errors.New("核对返回空记录，已拒绝删除")
	}
	info := aliyunRecordInfo{
		RecordID: dereferenceString(record.RecordId), DomainName: dereferenceString(record.DomainName),
		RR: dereferenceString(record.RR), Type: dereferenceString(record.Type), Value: dereferenceString(record.Value),
	}
	if strings.TrimSpace(info.RecordID) == "" || !matchesAliyunAuthorizationRecord(info, info.RecordID, zone, rr, value) {
		return errors.New("核对记录与本次随机授权探测不一致，已拒绝删除")
	}
	return a.Delete(ctx, info.RecordID)
}
