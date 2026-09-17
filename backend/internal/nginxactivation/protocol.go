package nginxactivation

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

const protocolVersion = 1
const maxFrameSize = 2 << 20

type request struct {
	Version int    `json:"version"`
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Chain   []byte `json:"chain,omitempty"`
	Key     []byte `json:"key,omitempty"`
}

type response struct {
	Version int         `json:"version"`
	Code    string      `json:"code"`
	ID      string      `json:"id,omitempty"`
	State   *State      `json:"state,omitempty"`
	Ready   *ReadyState `json:"ready,omitempty"`
}

// 固定长度前缀避免依赖连接 EOF；先校验上限再分配请求缓冲。
func readFrame(reader io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > maxFrameSize {
		return errors.New("本地激活消息大小无效")
	}
	data := make([]byte, int(length))
	defer clear(data)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("本地激活消息包含多余内容")
	}
	return nil
}

func writeFrame(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(data)
	if len(data) == 0 || len(data) > maxFrameSize {
		return errors.New("本地激活消息超过限制")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, data)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
