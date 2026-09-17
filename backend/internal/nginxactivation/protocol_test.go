package nginxactivation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func framedBytes(data []byte) []byte {
	framed := make([]byte, 4, len(data)+4)
	binary.BigEndian.PutUint32(framed, uint32(len(data)))
	return append(framed, data...)
}

func TestLocalProtocolBoundsAndSchema(t *testing.T) {
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, maxFrameSize+1)
	for _, data := range [][]byte{
		{}, {0, 0}, {0, 0, 0, 0}, oversized,
		{0, 0, 0, 10, '{'},
		framedBytes([]byte(`{"version":1,"action":"state","command":"nginx"}`)),
		framedBytes([]byte(`{"version":1,"action":"state"}{}`)),
		framedBytes([]byte(`[]`)),
	} {
		var message request
		if err := readFrame(bytes.NewReader(data), &message); err == nil {
			t.Fatalf("不应接受消息 %q", data)
		}
	}
	want := request{Version: 1, Action: "stage", Chain: []byte("certificate"), Key: []byte("private")}
	var stream bytes.Buffer
	if err := writeFrame(&stream, want); err != nil {
		t.Fatal(err)
	}
	var got request
	if err := readFrame(&stream, &got); err != nil || got.Action != want.Action || !bytes.Equal(got.Key, want.Key) || !bytes.Equal(got.Chain, want.Chain) {
		t.Fatalf("消息往返不一致：%+v, %v", got, err)
	}
	if err := writeFrame(io.Discard, request{Chain: make([]byte, maxFrameSize)}); err == nil {
		t.Fatal("不应编码超过上限的帧")
	}
}

type partialWriter struct { bytes.Buffer }

func (w *partialWriter) Write(data []byte) (int, error) {
	if len(data) > 2 {
		data = data[:2]
	}
	return w.Buffer.Write(data)
}

type stalledWriter struct{}

func (stalledWriter) Write([]byte) (int, error) { return 0, nil }

func TestLocalProtocolHandlesShortWrites(t *testing.T) {
	writer := &partialWriter{}
	if err := writeFrame(writer, response{Version: 1, Code: "ok"}); err != nil {
		t.Fatal(err)
	}
	var reply response
	if err := readFrame(&writer.Buffer, &reply); err != nil || reply.Code != "ok" {
		t.Fatal("短写导致响应丢失", err)
	}
	if err := writeFrame(stalledWriter{}, reply); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("无进展写入应退出", err)
	}
}
