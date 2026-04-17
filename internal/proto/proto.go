package proto

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

type MsgType string

const (
	MsgHello     MsgType = "HELLO"
	MsgHelloAck  MsgType = "HELLO_ACK"
	MsgHeartbeat MsgType = "HEARTBEAT"
	MsgError     MsgType = "ERROR"
)

type Envelope struct {
	Type MsgType         `json:"type"`
	Data json.RawMessage `json:"data"`
}

type HelloMsg struct {
	Token    string `json:"token"`
	TunnelID string `json:"tunnel_id"`
}

func (h HelloMsg) Validate() error {
	if h.Token == "" {
		return fmt.Errorf("proto: HelloMsg missing token")
	}
	if h.TunnelID == "" {
		return fmt.Errorf("proto: HelloMsg missing tunnel_id")
	}
	return nil
}

type HelloAckMsg struct {
	SessionID string `json:"session_id"`
	Hostname  string `json:"hostname"`
}

type ErrorMsg struct {
	Message string `json:"message"`
}

// HeartbeatMsg is a keep-alive ping exchanged on the control stream.
type HeartbeatMsg struct {
	Seq int64 `json:"seq"`
}

var readerPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(nil, 4096) },
}

func Write(w io.Writer, msgType MsgType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("proto: marshal payload: %w", err)
	}

	env := Envelope{Type: msgType, Data: data}
	line, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("proto: marshal envelope: %w", err)
	}

	_, err = fmt.Fprintf(w, "%s\n", line)
	return err
}


func Read(r io.Reader) (Envelope, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = readerPool.Get().(*bufio.Reader)
		br.Reset(r)
		defer readerPool.Put(br)
	}

	line, err := br.ReadBytes('\n')
	if err != nil {
		return Envelope{}, fmt.Errorf("proto: read: %w", err)
	}

	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Envelope{}, fmt.Errorf("proto: unmarshal: %w", err)
	}

	return env, nil
}

func Decode(env Envelope, target any) error {
	return json.Unmarshal(env.Data, target)
}