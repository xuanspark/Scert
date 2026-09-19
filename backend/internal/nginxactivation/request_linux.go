package nginxactivation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// 请求记录永久保留，防止已取消或已结束的请求在后续续期时迟到执行。
type stageRequest struct {
	ID         string `json:"id"`
	BaselineID string `json:"baseline_id,omitempty"`
	Status     string `json:"status"`
}

func readStageRequest(root *os.Root, id string) (stageRequest, error) {
	var value stageRequest
	data, err := readRegular(root, "request-"+id+".json", 4096)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return value, errors.New("暂存请求记录包含多余内容")
	}
	if value.ID != id || (value.BaselineID != "" && !validID(value.BaselineID)) || value.BaselineID == id || (value.Status != "reserved" && value.Status != "canceled") {
		return value, errors.New("暂存请求记录无效")
	}
	return value, nil
}

func saveStageRequest(root *os.Root, value stageRequest) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeAtomic(root, "request-"+value.ID+".json", data)
}

// StageRequest 先持久化请求占位再执行暂存；同一编号不能重放，即使首次未完成。
func (r Rotation) StageRequest(ctx context.Context, id string, chain, key []byte) (string, error) {
	if !validID(id) {
		return "", errors.New("暂存请求编号无效")
	}
	root, lock, err := r.open(ctx)
	if err != nil {
		return "", err
	}
	defer root.Close()
	defer lock.Close()
	if _, err := readStageRequest(root, id); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(errors.New("暂存请求已处理或记录不可信，拒绝重放"), err)
	}
	previous, err := readTransaction(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if previous.ID == id || previous.Status == "pending" || previous.Status == "staged" {
		return "", errors.New("暂存请求与已有事务冲突")
	}
	if err := saveStageRequest(root, stageRequest{ID: id, BaselineID: previous.ID, Status: "reserved"}); err != nil {
		return "", err
	}
	return r.stage(ctx, root, id, chain, key)
}

// ResolveRequest 与暂存持有同一把锁；没有进入事务的请求先落盘取消栅栏再返回。
// 已进入事务时仅返回可核对的状态，双层恢复仍由协调器驱动。
func (r Rotation) ResolveRequest(ctx context.Context, id string) (State, error) {
	if !validID(id) {
		return State{}, errors.New("暂存请求编号无效")
	}
	root, lock, err := r.open(ctx)
	if err != nil {
		return State{}, err
	}
	defer root.Close()
	defer lock.Close()
	value, err := readStageRequest(root, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	current, stateErr := readTransaction(root)
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return State{}, stateErr
	}
	if value.Status == "canceled" {
		if current.ID == id {
			return State{}, errors.New("取消栅栏与代理事务冲突，拒绝推断")
		}
		// 先前写入可能在重命名后同步失败；重试必须重新确认目录持久化。
		if err := saveStageRequest(root, value); err != nil {
			return State{}, err
		}
		return State{ID: id, Status: "canceled"}, nil
	}
	if err == nil {
		if current.ID == id {
			if err := saveTransaction(root, current); err != nil {
				return State{}, err
			}
			return transactionState(current), nil
		}
		if current.ID != value.BaselineID {
			return State{}, errors.New("暂存请求的基准事务发生变化，保留记录")
		}
	} else if current.ID == id {
		return State{}, errors.New("代理事务缺少对应请求记录，拒绝推断")
	}
	value = stageRequest{ID: id, Status: "canceled"}
	if err := saveStageRequest(root, value); err != nil {
		return State{}, err
	}
	return State{ID: id, Status: "canceled"}, nil
}
