package nginxactivation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Policy 必须由 root 独立维护，不能复用 Scert 可写的应用配置。
type Policy struct {
	Version         int     `json:"version"`
	AllowedUID      *uint32 `json:"allowed_uid"`
	SocketPath      string  `json:"socket_path"`
	Directory       string  `json:"directory"`
	Hostname        string  `json:"hostname"`
	ProxyAddress    string  `json:"proxy_address"`
	UpstreamAddress string  `json:"upstream_address"`
	NginxBinary     string  `json:"nginx_binary"`
	NginxConfig     string  `json:"nginx_config"`
	NginxPrefix     string  `json:"nginx_prefix"`
}

func LoadPolicy(path string) (Policy, error) {
	return loadPolicy(path, 0)
}

func loadPolicy(path string, owner uint32) (Policy, error) {
	var policy Policy
	data, err := readTrustedPolicy(path, owner)
	if err != nil {
		return policy, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return policy, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return policy, errors.New("激活策略包含多余内容")
	}
	if policy.Version != 1 || policy.AllowedUID == nil || *policy.AllowedUID == 0 {
		return policy, errors.New("激活策略版本或非特权调用者 UID 无效")
	}
	if !filepath.IsAbs(policy.SocketPath) || filepath.Clean(policy.SocketPath) != policy.SocketPath || policy.SocketPath == "/" || len(policy.SocketPath) > 107 {
		return policy, errors.New("激活 socket 路径无效")
	}
	if err := validateTrustedParents(filepath.Dir(policy.SocketPath), owner); err != nil {
		return policy, err
	}
	if err := validateDirectory(policy.Directory); err != nil {
		return policy, err
	}
	rotation := policy.Rotation()
	if err := errors.Join(rotation.Proxy.validate(), rotation.Upstream.validate()); err != nil {
		return policy, err
	}
	if policy.ProxyAddress == policy.UpstreamAddress {
		return policy, errors.New("代理和上游不能使用同一地址")
	}
	if err := validateOwnedFile(policy.NginxBinary, owner, true); err != nil {
		return policy, err
	}
	if err := validateOwnedFile(policy.NginxConfig, owner, false); err != nil {
		return policy, err
	}
	if !filepath.IsAbs(policy.NginxPrefix) || filepath.Clean(policy.NginxPrefix) != policy.NginxPrefix {
		return policy, errors.New("Nginx 前缀必须是规范绝对目录")
	}
	if err := validateTrustedParents(policy.NginxPrefix, owner); err != nil {
		return policy, err
	}
	return policy, nil
}

func (p Policy) Rotation() Rotation {
	return Rotation{
		Directory: p.Directory,
		Proxy:     Endpoint{Address: p.ProxyAddress, Hostname: p.Hostname},
		Upstream:  Endpoint{Address: p.UpstreamAddress, Hostname: p.Hostname},
		Reloader:  CommandReloader{Binary: p.NginxBinary, Config: p.NginxConfig, Prefix: p.NginxPrefix},
	}
}

func validateOwnedFile(path string, owner uint32, executable bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("激活策略文件必须使用规范绝对路径")
	}
	if err := validateTrustedParents(filepath.Dir(path), owner); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != owner || stat.Nlink != 1 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("激活策略文件类型、所有者或写权限不可信")
	}
	if executable && (info.Mode().Perm()&0o100 == 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0) {
		return errors.New("Nginx 可执行文件权限无效")
	}
	return nil
}

func readTrustedPolicy(path string, owner uint32) ([]byte, error) {
	if err := validateOwnedFile(path, owner, false); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != owner || stat.Nlink != 1 || info.Mode().Perm()&0o022 != 0 || info.Size() > 16<<10 {
		return nil, errors.New("激活策略文件不可信或超过限制")
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(data) > 16<<10 {
		return nil, errors.New("读取激活策略失败或超过限制")
	}
	return data, nil
}
