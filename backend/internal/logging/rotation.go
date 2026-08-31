package logging

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	defaultMaxDailyBytes int64 = 5 * 1024 * 1024
	defaultRetentionDays       = 30
)

var archivedLogPattern = regexp.MustCompile(`^scert-[0-9]{8}(?:-[0-9]{2})?\.log$`)

type dailyFileWriter struct {
	mu            sync.Mutex
	directory     string
	activePath    string
	file          *os.File
	day           string
	size          int64
	maxDailyBytes int64
	retentionDays int
	now           func() time.Time
}

func newDailyFileWriter(directory string) (*dailyFileWriter, error) {
	return newDailyFileWriterWithOptions(directory, defaultMaxDailyBytes, defaultRetentionDays, time.Now)
}

func newDailyFileWriterWithOptions(directory string, maxDailyBytes int64, retentionDays int, now func() time.Time) (*dailyFileWriter, error) {
	if maxDailyBytes < 1 {
		return nil, errors.New("日志每日容量上限必须大于 0")
	}
	if retentionDays < 1 {
		return nil, errors.New("日志保留天数必须大于 0")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, fmt.Errorf("创建日志目录失败：%w", err)
	}
	if err := os.Chmod(directory, 0o750); err != nil {
		return nil, fmt.Errorf("设置日志目录权限失败：%w", err)
	}
	writer := &dailyFileWriter{
		directory:     directory,
		activePath:    filepath.Join(directory, "scert.log"),
		maxDailyBytes: maxDailyBytes,
		retentionDays: retentionDays,
		now:           now,
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	if err := writer.cleanupExpired(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	return writer, nil
}

func (w *dailyFileWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	currentDay := logDay(w.now())
	if currentDay != w.day {
		if err := w.rotateLocked(w.day); err != nil {
			return 0, err
		}
		if err := w.openLocked(currentDay); err != nil {
			return 0, err
		}
		if err := w.cleanupExpiredLocked(); err != nil {
			return 0, err
		}
	}
	if int64(len(value)) > w.maxDailyBytes-w.size {
		return len(value), nil
	}
	written, err := w.file.Write(value)
	w.size += int64(written)
	return written, err
}

func (w *dailyFileWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := errors.Join(w.file.Sync(), w.file.Close())
	w.file = nil
	return err
}

func (w *dailyFileWriter) open() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	currentDay := logDay(w.now())
	if info, err := os.Lstat(w.activePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("活动日志路径必须是普通文件，不能使用符号链接")
		}
		fileDay := info.ModTime().Local().Format("20060102")
		if fileDay != currentDay {
			if err := w.rotateExistingLocked(fileDay); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查活动日志失败：%w", err)
	}
	return w.openLocked(currentDay)
}

func (w *dailyFileWriter) openLocked(day string) error {
	file, err := os.OpenFile(w.activePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开活动日志失败：%w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("设置活动日志权限失败：%w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("读取活动日志状态失败：%w", err)
	}
	w.file = file
	w.day = day
	w.size = info.Size()
	return nil
}

func (w *dailyFileWriter) rotateLocked(day string) error {
	if w.file != nil {
		if err := errors.Join(w.file.Sync(), w.file.Close()); err != nil {
			return fmt.Errorf("关闭活动日志失败：%w", err)
		}
		w.file = nil
	}
	return w.rotateExistingLocked(day)
}

func (w *dailyFileWriter) rotateExistingLocked(day string) error {
	if _, err := os.Lstat(w.activePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("检查待轮转日志失败：%w", err)
	}
	if day == "" {
		day = logDay(w.now())
	}
	for index := 0; index < 100; index++ {
		name := fmt.Sprintf("scert-%s.log", day)
		if index > 0 {
			name = fmt.Sprintf("scert-%s-%02d.log", day, index)
		}
		destination := filepath.Join(w.directory, name)
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(w.activePath, destination); err != nil {
				return fmt.Errorf("轮转活动日志失败：%w", err)
			}
			return nil
		} else if err != nil {
			return fmt.Errorf("检查轮转日志失败：%w", err)
		}
	}
	return errors.New("同一天的日志轮转文件数量已达到上限")
}

func (w *dailyFileWriter) cleanupExpired() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cleanupExpiredLocked()
}

func (w *dailyFileWriter) cleanupExpiredLocked() error {
	entries, err := os.ReadDir(w.directory)
	if err != nil {
		return fmt.Errorf("读取日志目录失败：%w", err)
	}
	cutoff := w.now().AddDate(0, 0, -w.retentionDays)
	for _, entry := range entries {
		if !archivedLogPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("读取历史日志状态失败：%w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(w.directory, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("删除过期日志失败：%w", err)
		}
	}
	return nil
}

func logDay(value time.Time) string {
	return value.Local().Format("20060102")
}
