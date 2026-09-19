package nginxactivation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

func leafHash(der []byte) string {
	hash := sha256.Sum256(der)
	return hex.EncodeToString(hash[:])
}

func transactionState(current transaction) State {
	return State{ID: current.ID, Status: current.Status, PreviousHash: leafHash(current.PreviousLeaf), CandidateHash: leafHash(current.CandidateLeaf)}
}

func (r Rotation) State(ctx context.Context) (State, error) {
	root, lock, err := r.open(ctx)
	if err != nil {
		return State{}, err
	}
	defer root.Close()
	defer lock.Close()
	current, err := readTransaction(root)
	if errors.Is(err, os.ErrNotExist) {
		return State{Status: "idle"}, nil
	}
	if err != nil {
		return State{}, err
	}
	return transactionState(current), nil
}

// Ready 在申请前核对固定策略、双层入口、专用材料和 Nginx 配置。
func (r Rotation) Ready(ctx context.Context) (ReadyState, error) {
	root, lock, err := r.open(ctx)
	if err != nil {
		return ReadyState{}, err
	}
	defer root.Close()
	defer lock.Close()
	current, err := readTransaction(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ReadyState{}, err
	}
	if err == nil && (current.Status == "pending" || current.Status == "staged") {
		return ReadyState{}, errors.New("管理证书仍有未完成事务")
	}
	leaf, err := r.Proxy.Check(ctx, nil)
	if err != nil {
		return ReadyState{}, err
	}
	if _, err := r.Upstream.Check(ctx, leaf); err != nil {
		return ReadyState{}, err
	}
	include, err := readRegular(root, includeName, 4096)
	if err != nil {
		return ReadyState{}, err
	}
	if err := verifyIncludeMaterial(root, include, leaf); err != nil {
		return ReadyState{}, err
	}
	if err := r.Reloader.Check(ctx); err != nil {
		return ReadyState{}, err
	}
	if err := requireInclude(root, include); err != nil {
		return ReadyState{}, err
	}
	return ReadyState{Hostname: r.Proxy.Hostname, ProxyAddress: r.Proxy.Address, UpstreamAddress: r.Upstream.Address, LeafHash: leafHash(leaf), RequestFencing: true}, nil
}

// Reconcile 仅根据严格验证的实际上游完成未决事务，不推断配置保存是否成功。
// 上游不可用、外部改动或证书不属于事务时保留记录，交由协调者处理后重试。
func (r Rotation) Reconcile(ctx context.Context, id string) (State, error) {
	root, lock, err := r.open(ctx)
	if err != nil {
		return State{}, err
	}
	defer root.Close()
	defer lock.Close()
	current, err := readTransaction(root)
	if err != nil {
		return State{}, err
	}
	if id == "" || id != current.ID {
		return State{}, errors.New("恢复事务编号不匹配")
	}
	state := transactionState(current)
	if current.Status == "committed" || current.Status == "rolled_back" {
		return state, nil
	}
	upstream, err := r.Upstream.Check(ctx, nil)
	if err != nil {
		return state, err
	}
	if bytes.Equal(upstream, current.PreviousLeaf) {
		if err := r.rollback(ctx, root, current); err != nil {
			return state, err
		}
		current.Status = "rolled_back"
		return transactionState(current), nil
	}
	if !bytes.Equal(upstream, current.CandidateLeaf) {
		return state, errors.New("上游证书不属于未决事务，拒绝自动恢复")
	}
	if err := requireInclude(root, current.CandidateInclude); err != nil {
		return state, err
	}
	if err := verifyIncludeMaterial(root, current.CandidateInclude, current.CandidateLeaf); err != nil {
		return state, err
	}
	if err := r.Proxy.Wait(ctx, current.CandidateLeaf); err != nil {
		return state, err
	}
	if _, err := r.Upstream.Check(ctx, current.CandidateLeaf); err != nil {
		return state, err
	}
	if err := requireInclude(root, current.CandidateInclude); err != nil {
		return state, err
	}
	current.Status = "committed"
	if err := saveTransaction(root, current); err != nil {
		return state, err
	}
	return transactionState(current), nil
}
