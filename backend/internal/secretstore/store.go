package secretstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const storeVersion = 1

type encryptedItem struct {
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type fileData struct {
	Version int                      `json:"version"`
	Items   map[string]encryptedItem `json:"items"`
}

type Store struct {
	mu       sync.Mutex
	key      []byte
	filePath string
}

// Metadata 仅包含敏感信息引用及更新时间，不包含密文或明文内容。
type Metadata struct {
	Reference string    `json:"reference"`
	UpdatedAt time.Time `json:"updated_at"`
}

func Open(configDir string) (*Store, error) {
	if configDir == "" {
		return nil, errors.New("敏感信息目录不能为空")
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建敏感信息目录失败：%w", err)
	}
	key, err := loadOrCreateKey(filepath.Join(configDir, "master.key"))
	if err != nil {
		return nil, err
	}
	return &Store{key: key, filePath: filepath.Join(configDir, "secrets.json")}, nil
}

func (s *Store) Put(ctx context.Context, reference string, value map[string]string) error {
	if s == nil {
		return errors.New("敏感信息存储未初始化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reference == "" {
		return errors.New("敏感信息引用不能为空")
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("编码敏感信息失败：%w", err)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return fmt.Errorf("初始化敏感信息加密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("初始化敏感信息加密模式失败：%w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("生成敏感信息随机数失败：%w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(reference))

	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked()
	if err != nil {
		return err
	}
	data.Items[reference] = encryptedItem{
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
		UpdatedAt:  time.Now().UTC(),
	}
	return s.writeLocked(data)
}

func (s *Store) Get(ctx context.Context, reference string) (map[string]string, error) {
	if s == nil {
		return nil, errors.New("敏感信息存储未初始化")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	item, ok := data.Items[reference]
	if !ok {
		return nil, fmt.Errorf("敏感信息引用不存在：%s", reference)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(item.Nonce)
	if err != nil {
		return nil, fmt.Errorf("解析敏感信息随机数失败：%w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(item.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("解析敏感信息密文失败：%w", err)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, fmt.Errorf("初始化敏感信息解密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化敏感信息解密模式失败：%w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(reference))
	if err != nil {
		return nil, fmt.Errorf("解密敏感信息失败：%w", err)
	}
	var result map[string]string
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("解析敏感信息失败：%w", err)
	}
	return result, nil
}

// Has 检查敏感信息引用是否存在，但不解密内容。
func (s *Store) Has(ctx context.Context, reference string) (bool, error) {
	if s == nil {
		return false, errors.New("敏感信息存储未初始化")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if reference == "" {
		return false, errors.New("敏感信息引用不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked()
	if err != nil {
		return false, err
	}
	_, ok := data.Items[reference]
	return ok, nil
}

// List 按引用前缀返回敏感信息元数据，不解密或返回敏感内容。
func (s *Store) List(ctx context.Context, prefix string) ([]Metadata, error) {
	if s == nil {
		return nil, errors.New("敏感信息存储未初始化")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0)
	for reference, item := range data.Items {
		if prefix == "" || strings.HasPrefix(reference, prefix) {
			result = append(result, Metadata{Reference: reference, UpdatedAt: item.UpdatedAt})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Reference < result[j].Reference })
	return result, nil
}

// Delete 原子删除指定引用；引用不存在时返回明确错误。
func (s *Store) Delete(ctx context.Context, reference string) error {
	if s == nil {
		return errors.New("敏感信息存储未初始化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return errors.New("敏感信息引用不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.readLocked()
	if err != nil {
		return err
	}
	if _, ok := data.Items[reference]; !ok {
		return fmt.Errorf("敏感信息引用不存在：%s", reference)
	}
	delete(data.Items, reference)
	return s.writeLocked(data)
}

func (s *Store) readLocked() (fileData, error) {
	data := fileData{Version: storeVersion, Items: map[string]encryptedItem{}}
	content, err := os.ReadFile(s.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return fileData{}, fmt.Errorf("读取敏感信息文件失败：%w", err)
	}
	if err := json.Unmarshal(content, &data); err != nil {
		return fileData{}, fmt.Errorf("解析敏感信息文件失败：%w", err)
	}
	if data.Version != storeVersion {
		return fileData{}, fmt.Errorf("敏感信息文件版本不兼容：%d", data.Version)
	}
	if data.Items == nil {
		data.Items = map[string]encryptedItem{}
	}
	return data, nil
}

func (s *Store) writeLocked(data fileData) error {
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("编码敏感信息文件失败：%w", err)
	}
	content = append(content, '\n')
	if err := writeAtomicFile(s.filePath, content, 0o600, os.Rename); err != nil {
		return fmt.Errorf("写入敏感信息文件失败：%w", err)
	}
	if err := os.Chmod(s.filePath, 0o600); err != nil {
		return fmt.Errorf("设置敏感信息文件权限失败：%w", err)
	}
	return nil
}

func writeAtomicFile(path string, content []byte, mode fs.FileMode, renameFile func(string, string) error) error {
	if renameFile == nil {
		renameFile = os.Rename
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建敏感信息临时文件失败：%w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("设置敏感信息临时文件权限失败：%w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		return fmt.Errorf("写入敏感信息临时文件失败：%w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步敏感信息临时文件失败：%w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("关闭敏感信息临时文件失败：%w", err)
	}
	closed = true
	if err := renameFile(temporaryPath, path); err != nil {
		return fmt.Errorf("原子替换敏感信息文件失败：%w", err)
	}
	committed = true
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("同步敏感信息目录失败：%w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func loadOrCreateKey(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := base64.RawStdEncoding.DecodeString(string(content))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("配置主密钥格式无效")
		}
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("收紧配置主密钥权限失败：%w", chmodErr)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取配置主密钥失败：%w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成配置主密钥失败：%w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("创建配置主密钥失败：%w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("写入配置主密钥失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("关闭配置主密钥文件失败：%w", err)
	}
	return key, nil
}
