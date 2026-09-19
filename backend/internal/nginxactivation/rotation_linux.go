package nginxactivation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/certificate"
	"git.pcux.cn/xuans/Scert/backend/internal/processlock"
)

const (
	includeName = "active.conf"
	journalName = "transaction.json"
)

type Reloader interface {
	Check(context.Context) error
	Reload(context.Context) error
}

// Rotation 的目录与命令必须由本地受信部署策略提供，不能来自 HTTP 或证书请求。
// 调用方须先安装引用 active.conf 的专用站点，并将目录交给独立激活器用户。
type Rotation struct {
	Directory string
	Proxy     Endpoint
	Upstream  Endpoint
	Reloader  Reloader
}

type transaction struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	PreviousInclude  []byte `json:"previous_include"`
	CandidateInclude []byte `json:"candidate_include"`
	PreviousLeaf     []byte `json:"previous_leaf"`
	CandidateLeaf    []byte `json:"candidate_leaf"`
}

// Stage 只切换 Nginx 并验证入口；上游切换后必须调用 Commit 才算双层激活完成。
// 任意中断均保留持久化记录，下一次启动应先调用 Recover。
func (r Rotation) Stage(ctx context.Context, chain, key []byte) (id string, resultErr error) {
	root, lock, err := r.open(ctx)
	if err != nil {
		return "", err
	}
	defer root.Close()
	defer lock.Close()
	return r.stage(ctx, root, "", chain, key)
}

func (r Rotation) stage(ctx context.Context, root *os.Root, id string, chain, key []byte) (_ string, resultErr error) {
	previous, err := readTransaction(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && (previous.Status == "pending" || previous.Status == "staged") {
		return "", errors.New("仍有未完成的管理证书激活，请先恢复")
	}
	leaf, err := r.validateMaterial(ctx, chain, key)
	if err != nil {
		return "", err
	}
	oldLeaf, err := r.Proxy.Check(ctx, nil)
	if err != nil {
		return "", err
	}
	oldCertificate, err := x509.ParseCertificate(oldLeaf)
	if err != nil || !leaf.NotAfter.After(oldCertificate.NotAfter) {
		return "", errors.New("候选管理证书没有延长入口实际有效期")
	}
	if _, err := r.Upstream.Check(ctx, oldLeaf); err != nil {
		return "", err
	}
	oldInclude, err := readRegular(root, includeName, 4096)
	if err != nil {
		return "", err
	}
	if err := verifyIncludeMaterial(root, oldInclude, oldLeaf); err != nil {
		return "", err
	}
	if err := r.Reloader.Check(ctx); err != nil {
		return "", fmt.Errorf("Nginx 原配置检查失败：%w", err)
	}
	if id == "" {
		id, err = newID()
		if err != nil {
			return "", err
		}
	}
	if err := root.Mkdir(id, 0o700); err != nil {
		return "", err
	}
	if err := writeExclusive(root, filepath.Join(id, "fullchain.pem"), chain); err != nil {
		return "", err
	}
	if err := writeExclusive(root, filepath.Join(id, "key.pem"), key); err != nil {
		return "", err
	}
	// 先同步新版本目录，再持久化引用，避免断电后只留下不存在的证书路径。
	versionDir, err := root.Open(id)
	if err != nil {
		return "", err
	}
	if err := errors.Join(versionDir.Sync(), versionDir.Close()); err != nil {
		return "", err
	}
	current := transaction{
		ID: id, Status: "pending", PreviousInclude: oldInclude, PreviousLeaf: oldLeaf,
		CandidateLeaf:    leaf.Raw,
		CandidateInclude: renderInclude(r.Directory, id),
	}
	if err := saveTransaction(root, current); err != nil {
		return "", err
	}
	defer func() {
		if resultErr != nil {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, r.rollback(rollbackCtx, root, current))
		}
	}()
	if err := requireInclude(root, current.PreviousInclude); err != nil {
		return id, err
	}
	if err := writeAtomic(root, includeName, current.CandidateInclude); err != nil {
		return id, err
	}
	if err := r.Reloader.Check(ctx); err != nil {
		return id, fmt.Errorf("Nginx 候选配置检查失败：%w", err)
	}
	if err := requireInclude(root, current.CandidateInclude); err != nil {
		return id, err
	}
	if err := r.Reloader.Reload(ctx); err != nil {
		return id, fmt.Errorf("Nginx 重载失败：%w", err)
	}
	if err := r.Proxy.Wait(ctx, current.CandidateLeaf); err != nil {
		return id, err
	}
	if err := requireInclude(root, current.CandidateInclude); err != nil {
		return id, err
	}
	current.Status = "staged"
	return id, saveTransaction(root, current)
}

// Commit 验证代理和上游均提供同一候选证书且就绪；失败时保留事务供协调者回退。
func (r Rotation) Commit(ctx context.Context, id string) error {
	root, lock, err := r.open(ctx)
	if err != nil {
		return err
	}
	defer root.Close()
	defer lock.Close()
	current, err := readTransaction(root)
	if err != nil {
		return err
	}
	if id == "" || current.ID != id || current.Status != "staged" {
		return errors.New("管理证书激活事务不匹配或尚未完成暂存")
	}
	include, err := readRegular(root, includeName, 4096)
	if err != nil || !bytes.Equal(include, current.CandidateInclude) {
		return errors.New("管理证书专用引用已被修改，拒绝提交")
	}
	if err := verifyIncludeMaterial(root, include, current.CandidateLeaf); err != nil {
		return err
	}
	if err := r.Upstream.Wait(ctx, current.CandidateLeaf); err != nil {
		return err
	}
	if err := r.Proxy.Wait(ctx, current.CandidateLeaf); err != nil {
		return err
	}
	if err := requireInclude(root, current.CandidateInclude); err != nil {
		return err
	}
	current.Status = "committed"
	return saveTransaction(root, current)
}

func (r Rotation) Rollback(ctx context.Context, id string) error {
	return r.restore(ctx, id, false)
}

// Recover 处理进程退出时残留的事务，不回退已经提交的双层证书。
func (r Rotation) Recover(ctx context.Context) error {
	return r.restore(ctx, "", true)
}

func (r Rotation) restore(ctx context.Context, id string, recoverOnly bool) error {
	root, lock, err := r.open(ctx)
	if err != nil {
		return err
	}
	defer root.Close()
	defer lock.Close()
	current, err := readTransaction(root)
	if errors.Is(err, os.ErrNotExist) && recoverOnly {
		return nil
	}
	if err != nil {
		return err
	}
	if recoverOnly && (current.Status == "committed" || current.Status == "rolled_back") {
		return nil
	}
	if !recoverOnly && (id == "" || current.ID != id) {
		return errors.New("管理证书回退事务不匹配")
	}
	if current.Status == "committed" {
		return errors.New("已提交事务不能通过临时激活回退接口恢复")
	}
	return r.rollback(ctx, root, current)
}

func (r Rotation) rollback(ctx context.Context, root *os.Root, current transaction) error {
	active, err := readRegular(root, includeName, 4096)
	if err != nil {
		return err
	}
	if !bytes.Equal(active, current.CandidateInclude) && !bytes.Equal(active, current.PreviousInclude) {
		return errors.New("Nginx 证书引用发生外部变化，拒绝回退覆盖")
	}
	if err := verifyIncludeMaterial(root, current.PreviousInclude, current.PreviousLeaf); err != nil {
		return err
	}
	if err := writeAtomic(root, includeName, current.PreviousInclude); err != nil {
		return fmt.Errorf("恢复 Nginx 证书引用失败：%w", err)
	}
	if err := r.Reloader.Check(ctx); err != nil {
		return fmt.Errorf("Nginx 回退配置检查失败：%w", err)
	}
	if err := requireInclude(root, current.PreviousInclude); err != nil {
		return err
	}
	if err := r.Reloader.Reload(ctx); err != nil {
		return fmt.Errorf("Nginx 回退重载失败：%w", err)
	}
	if err := r.Proxy.Wait(ctx, current.PreviousLeaf); err != nil {
		return fmt.Errorf("Nginx 回退就绪检查失败：%w", err)
	}
	if err := requireInclude(root, current.PreviousInclude); err != nil {
		return err
	}
	current.Status = "rolled_back"
	return saveTransaction(root, current)
}

func (r Rotation) validateMaterial(ctx context.Context, chain, key []byte) (*x509.Certificate, error) {
	if len(chain) == 0 || len(chain) > 1<<20 || len(key) == 0 || len(key) > 64<<10 {
		return nil, errors.New("候选证书或私钥大小无效")
	}
	pair, err := tls.X509KeyPair(chain, key)
	if err != nil {
		return nil, errors.New("候选证书和私钥不匹配")
	}
	validator := certificate.LocalValidator{VerifyChain: true, Roots: r.Proxy.Roots}
	if _, err := validator.ValidatePEM(ctx, chain, []string{r.Proxy.Hostname}); err != nil {
		return nil, fmt.Errorf("候选证书未通过可信校验：%w", err)
	}
	validator.Roots = r.Upstream.Roots
	if _, err := validator.ValidatePEM(ctx, chain, []string{r.Upstream.Hostname}); err != nil {
		return nil, fmt.Errorf("候选证书未通过上游可信校验：%w", err)
	}
	if len(pair.Certificate[0]) > 16<<10 {
		return nil, errors.New("候选叶证书超过大小限制")
	}
	return x509.ParseCertificate(pair.Certificate[0])
}

func (r Rotation) open(ctx context.Context) (*os.Root, *processlock.Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r.Reloader == nil || r.Proxy.Address == r.Upstream.Address || r.Proxy.Hostname != r.Upstream.Hostname {
		return nil, nil, errors.New("Nginx 激活策略必须配置独立代理、上游和重载器")
	}
	if err := errors.Join(r.Proxy.validate(), r.Upstream.validate(), validateDirectory(r.Directory)); err != nil {
		return nil, nil, err
	}
	if info, err := os.Lstat(filepath.Join(r.Directory, ".activation.lock")); err == nil {
		if err := validateStateFile(info, 4096); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	lock, err := processlock.Acquire(filepath.Join(r.Directory, ".activation.lock"))
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(r.Directory)
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	return root, lock, nil
}

func readTransaction(root *os.Root) (transaction, error) {
	var current transaction
	data, err := readRegular(root, journalName, 64<<10)
	if err != nil {
		return current, err
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return current, err
	}
	if !validID(current.ID) || len(current.PreviousInclude) == 0 || len(current.PreviousInclude) > 4096 || len(current.PreviousLeaf) == 0 || len(current.PreviousLeaf) > 16<<10 || len(current.CandidateLeaf) == 0 || len(current.CandidateLeaf) > 16<<10 {
		return current, errors.New("管理证书激活记录不完整")
	}
	if !bytes.Equal(current.CandidateInclude, renderInclude(root.Name(), current.ID)) {
		return current, errors.New("管理证书激活记录的候选引用无效")
	}
	if _, err := includeVersion(root.Name(), current.PreviousInclude); err != nil {
		return current, err
	}
	for _, leaf := range [][]byte{current.PreviousLeaf, current.CandidateLeaf} {
		if _, err := x509.ParseCertificate(leaf); err != nil {
			return current, errors.New("管理证书激活记录包含无效证书")
		}
	}
	switch current.Status {
	case "pending", "staged", "committed", "rolled_back":
		return current, nil
	default:
		return current, errors.New("管理证书激活记录状态无效")
	}
}

func saveTransaction(root *os.Root, current transaction) error {
	data, err := json.Marshal(current)
	if err != nil {
		return err
	}
	return writeAtomic(root, journalName, data)
}

func validID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == id
}

func requireInclude(root *os.Root, expected []byte) error {
	actual, err := readRegular(root, includeName, 4096)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("管理证书专用引用发生外部变化")
	}
	return nil
}

func renderInclude(directory, id string) []byte {
	return []byte(fmt.Sprintf("ssl_certificate %s/%s/fullchain.pem;\nssl_certificate_key %s/%s/key.pem;\n", directory, id, directory, id))
}

func includeVersion(directory string, include []byte) (string, error) {
	fields := strings.Fields(string(include))
	if len(fields) != 4 {
		return "", errors.New("管理证书专用引用格式无效")
	}
	id := filepath.Base(filepath.Dir(fields[1]))
	if !validID(id) || !bytes.Equal(include, renderInclude(directory, id)) {
		return "", errors.New("管理证书必须引用激活器专用版本目录")
	}
	return id, nil
}

func verifyIncludeMaterial(root *os.Root, include, expected []byte) error {
	id, err := includeVersion(root.Name(), include)
	if err != nil {
		return err
	}
	if err := validateDirectory(filepath.Join(root.Name(), id)); err != nil {
		return err
	}
	chain, err := readRegular(root, filepath.Join(id, "fullchain.pem"), 1<<20)
	if err != nil {
		return err
	}
	key, err := readRegular(root, filepath.Join(id, "key.pem"), 64<<10)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(chain, key)
	if err != nil || !bytes.Equal(pair.Certificate[0], expected) {
		return errors.New("管理证书磁盘材料与已验证入口不一致")
	}
	return nil
}
