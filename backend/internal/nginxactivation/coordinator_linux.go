package nginxactivation

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
)

var ErrCoordinationPending = errors.New("管理证书协调仍未完成，已阻止再次申请证书")

type CertificateFiles struct {
	CertificatePath string `json:"certificate_path"`
	PrivateKeyPath  string `json:"private_key_path"`
}

type ManagedCertificate struct {
	CertificateFiles
	Hostname        string `json:"hostname"`
	Mode            string `json:"mode"`
	LeafHash        string `json:"leaf_hash"`
	ProxyAddress    string `json:"proxy_address"`
	UpstreamAddress string `json:"upstream_address"`
}

type ManagedSession interface {
	Snapshot() (ManagedCertificate, error)
	Activate(CertificateFiles, string) error
}

// ManagedUpstream 的回调必须独占所有证书切换及相关配置写入，但不能阻塞 TLS 和就绪探测。
type ManagedUpstream interface {
	WithCertificate(context.Context, func(ManagedSession) error) error
}

type ProxyClient interface {
	Ready(context.Context) (ReadyState, error)
	State(context.Context) (State, error)
	Stage(context.Context, []byte, []byte) (string, error)
	Commit(context.Context, string) error
	Reconcile(context.Context, string) (State, error)
}

// Coordinator 的目录归应用用户所有，与 root 激活器的专用目录分离。
type Coordinator struct {
	Directory string
	Proxy     ProxyClient
	Upstream  ManagedUpstream
}

func (c Coordinator) Ready(ctx context.Context) error {
	return c.withSession(ctx, func(root *os.Root, session ManagedSession) error {
		if _, err := c.recover(ctx, root, session); err != nil {
			return err
		}
		current, err := session.Snapshot()
		if err != nil {
			return err
		}
		return c.ready(ctx, current)
	})
}

func (c Coordinator) Recover(ctx context.Context) error {
	return c.withSession(ctx, func(root *os.Root, session ManagedSession) error {
		_, err := c.recover(ctx, root, session)
		return err
	})
}

func (c Coordinator) Activate(ctx context.Context, files CertificateFiles) error {
	return c.withSession(ctx, func(root *os.Root, session ManagedSession) (resultErr error) {
		if _, err := c.recover(ctx, root, session); err != nil {
			return err
		}
		previous, err := session.Snapshot()
		if err != nil {
			return err
		}
		if err := c.ready(ctx, previous); err != nil {
			return err
		}
		chain, err := readManagedMaterial(files.CertificatePath, 1<<20)
		if err != nil {
			return err
		}
		key, err := readManagedMaterial(files.PrivateKeyPath, 64<<10)
		if err != nil {
			return err
		}
		defer clear(key)
		pair, err := tls.X509KeyPair(chain, key)
		if err != nil {
			return errors.New("候选管理证书与私钥不匹配")
		}
		candidate := previous
		candidate.CertificateFiles, candidate.LeafHash = files, leafHash(pair.Certificate[0])
		baseline, err := c.Proxy.State(ctx)
		if err != nil {
			return err
		}
		record := coordinationRecord{Version: 1, Phase: "prepared", BaselineID: baseline.ID, Previous: previous, Candidate: candidate}
		if err := record.validate(); err != nil {
			return err
		}
		if err := saveCoordination(root, record); err != nil {
			return err
		}
		// 请求取消或应答丢失仍在同一独占区内恢复；不能把网络失败当成尚未执行。
		defer func() {
			if resultErr != nil {
				recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
				defer cancel()
				_, recoveryErr := c.recover(recoveryCtx, root, session)
				resultErr = errors.Join(resultErr, recoveryErr)
			}
		}()
		id, err := c.Proxy.Stage(ctx, chain, key)
		if err != nil {
			return err
		}
		if !validID(id) || id == record.BaselineID {
			return errors.New("代理返回的暂存事务编号无效")
		}
		record.ProxyID, record.Phase = id, "staged"
		if err := saveCoordination(root, record); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := session.Activate(files, candidate.LeafHash); err != nil {
			return err
		}
		record.Phase = "upstream"
		if err := saveCoordination(root, record); err != nil {
			return err
		}
		commitErr := c.Proxy.Commit(ctx, id)
		verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Second)
		defer cancel()
		completed, err := c.recover(verifyCtx, root, session)
		if err != nil || completed.Phase != "committed" {
			return errors.Join(commitErr, err, ErrCoordinationPending)
		}
		// 只有同一事务终态及实时双层入口均已验证，才接受丢失提交应答后的成功。
		return nil
	})
}

func (c Coordinator) recover(ctx context.Context, root *os.Root, session ManagedSession) (coordinationRecord, error) {
	record, err := readCoordination(root)
	if errors.Is(err, os.ErrNotExist) {
		return coordinationRecord{}, nil
	}
	if err != nil {
		return record, err
	}
	if record.Phase == "committed" || record.Phase == "rolled_back" {
		return record, nil
	}
	current, err := session.Snapshot()
	if err != nil {
		return record, errors.Join(ErrCoordinationPending, err)
	}
	if current != record.Previous && current != record.Candidate {
		return record, errors.Join(ErrCoordinationPending, errors.New("当前上游不属于持久化协调记录，拒绝覆盖"))
	}
	state, err := c.Proxy.State(ctx)
	if err != nil {
		return record, errors.Join(ErrCoordinationPending, err)
	}
	if !validID(state.ID) || state.ID == record.BaselineID ||
		(record.ProxyID != "" && state.ID != record.ProxyID) ||
		state.PreviousHash != record.Previous.LeafHash || state.CandidateHash != record.Candidate.LeafHash {
		// 尚未观测到对应事务时保留意图；延迟到达的暂存请求仍可能执行，不能清除或重放。
		return record, errors.Join(ErrCoordinationPending, errors.New("尚未确认对应的代理事务，保留协调记录"))
	}
	record.ProxyID = state.ID
	if state.Status == "pending" || state.Status == "staged" {
		state, err = c.Proxy.Reconcile(ctx, state.ID)
		if err != nil {
			return record, errors.Join(ErrCoordinationPending, err)
		}
	}
	if state.ID != record.ProxyID || state.PreviousHash != record.Previous.LeafHash || state.CandidateHash != record.Candidate.LeafHash {
		return record, errors.Join(ErrCoordinationPending, errors.New("代理恢复结果不属于当前事务"))
	}
	if (state.Status != "committed" || current != record.Candidate) && (state.Status != "rolled_back" || current != record.Previous) {
		return record, errors.Join(ErrCoordinationPending, errors.New("代理终态与实际上游证书不一致"))
	}
	if err := c.ready(ctx, current); err != nil {
		return record, errors.Join(ErrCoordinationPending, err)
	}
	record.Phase = state.Status
	return record, saveCoordination(root, record)
}

func (c Coordinator) ready(ctx context.Context, expected ManagedCertificate) error {
	if err := expected.validate(); err != nil {
		return err
	}
	ready, err := c.Proxy.Ready(ctx)
	if err != nil {
		return err
	}
	if ready.Hostname != expected.Hostname || ready.LeafHash != expected.LeafHash || ready.ProxyAddress != expected.ProxyAddress || ready.UpstreamAddress != expected.UpstreamAddress {
		return errors.New("本地激活策略或双层入口与应用管理配置不一致")
	}
	return nil
}

func (c Coordinator) withSession(ctx context.Context, operation func(*os.Root, ManagedSession) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Proxy == nil || c.Upstream == nil {
		return errors.New("管理证书协调器尚未配置")
	}
	if err := validatePrivateDirectory(c.Directory); err != nil {
		return err
	}
	lockPath := filepath.Join(c.Directory, ".coordination.lock")
	if info, err := os.Lstat(lockPath); err == nil {
		if err := validateStateFile(info, 4096); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock, err := processlock.Acquire(lockPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	return c.Upstream.WithCertificate(ctx, func(session ManagedSession) error {
		return operation(root, session)
	})
}

func readManagedMaterial(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("管理证书材料必须使用规范绝对路径")
	}
	if err := validateTrustedParents(filepath.Dir(path), uint32(os.Geteuid())); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := readRegular(root, filepath.Base(path), limit)
	if err != nil {
		return nil, fmt.Errorf("读取候选管理证书材料失败：%w", err)
	}
	return data, nil
}
