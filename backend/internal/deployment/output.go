package deployment

import "sync"

const commandOutputLimit = 64 << 10

// commandOutput 限制远程标准输出和错误输出占用，防止异常命令耗尽管理端内存。
type commandOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *commandOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := commandOutputLimit - len(b.data)
	if remaining > len(data) {
		remaining = len(data)
	}
	b.data = append(b.data, data[:remaining]...)
	return len(data), nil
}

func (b *commandOutput) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}
