//go:build !windows

package admin

func protectCodeFile(string) error {
	return nil
}
