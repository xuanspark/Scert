package localreset

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

const (
	inspectionPartSize     = 8 << 10
	maxInspectionParts     = 48
	maxInspectionBytes     = inspectionPartSize * maxInspectionParts
	maxInspectionPathBytes = 4096 + len(".scert-backup-") + 160
)

// 单帧仍限制为 16 KiB；总量覆盖十二条最长路径经 JSON 六倍转义后的大小。
// 分片不增加业务请求次数，不重复采样；任何缺片、乱序或超限均不返回部分核查结果。
type inspectionPart struct {
	Index int    `json:"index"`
	Total int    `json:"total"`
	Data  []byte `json:"data"`
}

func validInspectionPayload(value *deployment.RecoveryInspection) bool {
	if value == nil || !validRecoveryID(value.TransactionID) || len(value.Files) != 9 || len(value.Locks) != 3 || value.ObservedAt.IsZero() {
		return false
	}
	for _, file := range value.Files {
		if len(file.Path) > maxInspectionPathBytes {
			return false
		}
	}
	for _, lock := range value.Locks {
		if len(lock.Path) > maxInspectionPathBytes {
			return false
		}
	}
	return true
}

func writeRecoveryResponse(ctx context.Context, writer io.Writer, response Response) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if response.Inspection == nil {
		return writeFrame(writer, response)
	}
	if !validInspectionPayload(response.Inspection) || response.InspectionPart != nil || response.RecoveryPage != nil {
		return ErrProtocol
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return ErrProtocol
	}
	if len(encoded)+1 <= MaxFrameSize {
		return writeFrame(writer, response)
	}
	data, err := json.Marshal(response.Inspection)
	if err != nil || len(data) > maxInspectionBytes {
		return ErrProtocol
	}
	total := (len(data) + inspectionPartSize - 1) / inspectionPartSize
	response.Inspection = nil
	for index := 0; index < total; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := index * inspectionPartSize
		end := min(start+inspectionPartSize, len(data))
		response.InspectionPart = &inspectionPart{Index: index, Total: total, Data: data[start:end]}
		if err := writeFrame(writer, response); err != nil {
			return err
		}
	}
	return nil
}

func readInspectionParts(reader *bufio.Reader, first Response) (*deployment.RecoveryInspection, error) {
	if first.InspectionPart == nil || first.InspectionPart.Total < 1 || first.InspectionPart.Total > maxInspectionParts {
		return nil, ErrProtocol
	}
	total := first.InspectionPart.Total
	data := make([]byte, 0, total*inspectionPartSize)
	response := first
	for index := 0; index < total; index++ {
		if index > 0 {
			response = Response{}
			if err := readFrame(reader, &response); err != nil {
				return nil, ErrProtocol
			}
		}
		part := response.InspectionPart
		if !validChallenge(response) || response.Nonce != first.Nonce || len(response.Methods) != 0 || response.RecoveryAvailable ||
			response.Inspection != nil || response.RecoveryPage != nil || part == nil || part.Index != index || part.Total != total ||
			len(part.Data) == 0 || len(part.Data) > inspectionPartSize || (index < total-1 && len(part.Data) != inspectionPartSize) {
			return nil, ErrProtocol
		}
		data = append(data, part.Data...)
	}
	var inspection deployment.RecoveryInspection
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&inspection) != nil || !validInspectionPayload(&inspection) {
		return nil, ErrProtocol
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, ErrProtocol
	}
	return &inspection, nil
}
