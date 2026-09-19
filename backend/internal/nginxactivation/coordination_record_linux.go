package nginxactivation

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const coordinationName = "coordination.json"

type coordinationRecord struct {
	Version    int                `json:"version"`
	Phase      string             `json:"phase"`
	BaselineID string             `json:"baseline_id,omitempty"`
	ProxyID    string             `json:"proxy_id,omitempty"`
	Previous   ManagedCertificate `json:"previous"`
	Candidate  ManagedCertificate `json:"candidate"`
}

func (value ManagedCertificate) validate() error {
	hash, err := hex.DecodeString(value.LeafHash)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != value.LeafHash || value.Mode != "proxy" {
		return errors.New("管理证书协调摘要或模式无效")
	}
	if err := errors.Join((Endpoint{Address: value.ProxyAddress, Hostname: value.Hostname}).validate(), (Endpoint{Address: value.UpstreamAddress, Hostname: value.Hostname}).validate()); err != nil {
		return err
	}
	if value.ProxyAddress == value.UpstreamAddress {
		return errors.New("管理证书代理与上游地址不能相同")
	}
	for _, path := range []string{value.CertificatePath, value.PrivateKeyPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > 4096 {
			return errors.New("管理证书协调路径无效")
		}
	}
	return nil
}

func (record coordinationRecord) validate() error {
	if record.Version != 1 || (record.BaselineID != "" && !validID(record.BaselineID)) || (record.ProxyID != "" && !validID(record.ProxyID)) {
		return errors.New("管理证书协调记录版本或编号无效")
	}
	switch record.Phase {
	case "prepared":
	case "staged", "upstream", "committed", "rolled_back":
		if record.ProxyID == "" {
			return errors.New("管理证书协调记录缺少代理事务编号")
		}
	default:
		return errors.New("管理证书协调记录阶段无效")
	}
	if err := errors.Join(record.Previous.validate(), record.Candidate.validate()); err != nil {
		return err
	}
	previous, candidate := record.Previous, record.Candidate
	if previous.LeafHash == candidate.LeafHash || previous.CertificatePath == candidate.CertificatePath || previous.PrivateKeyPath == candidate.PrivateKeyPath {
		return errors.New("候选证书必须使用独立的新版本材料")
	}
	candidate.CertificateFiles, candidate.LeafHash = previous.CertificateFiles, previous.LeafHash
	if candidate != previous || (record.ProxyID != "" && record.ProxyID == record.BaselineID) {
		return errors.New("管理证书协调记录包含不一致的策略或事务编号")
	}
	return nil
}

func readCoordination(root *os.Root) (coordinationRecord, error) {
	var record coordinationRecord
	data, err := readRegular(root, coordinationName, 64<<10)
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return record, errors.New("管理证书协调记录包含多余内容")
	}
	return record, record.validate()
}

func saveCoordination(root *os.Root, record coordinationRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeAtomic(root, coordinationName, data)
}
