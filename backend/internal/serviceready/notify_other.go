//go:build !linux

package serviceready

// Ready 在不使用 Linux 服务管理器的平台保持原有启动行为。
func Ready(string) error {
	return nil
}
