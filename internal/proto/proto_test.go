package proto_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/oluu-web/lennut/internal/proto"
)

// --- Write / Read round-trip ---

func TestWriteRead_Hello(t *testing.T) {
	var buf bytes.Buffer
	msg := proto.HelloMsg{Token: "secret123", LocalPort: 3000}

	if err := proto.Write(&buf, proto.MsgHello, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	env, err := proto.Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if env.Type != proto.MsgHello {
		t.Fatalf("expected MsgHello, got %q", env.Type)
	}

	var got proto.HelloMsg
	if err := proto.Decode(env, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Token != msg.Token {
		t.Errorf("Token: want %q, got %q", msg.Token, got.Token)
	}
	if got.LocalPort != msg.LocalPort {
		t.Errorf("LocalPort: want %d, got %d", msg.LocalPort, got.LocalPort)
	}
}

func TestWriteRead_HelloAck(t *testing.T) {
	var buf bytes.Buffer
	msg := proto.HelloAckMsg{SessionID: "sess-1", Hostname: "abc.revtunnel.xyz"}

	if err := proto.Write(&buf, proto.MsgHelloAck, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	env, err := proto.Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if env.Type != proto.MsgHelloAck {
		t.Fatalf("expected MsgHelloAck, got %q", env.Type)
	}
	var got proto.HelloAckMsg
	proto.Decode(env, &got)
	if got.SessionID != "sess-1" || got.Hostname != "abc.revtunnel.xyz" {
		t.Errorf("unexpected HelloAck: %+v", got)
	}
}

func TestWriteRead_Heartbeat(t *testing.T) {
	var buf bytes.Buffer
	if err := proto.Write(&buf, proto.MsgHeartbeat, proto.HeartbeatMsg{Seq: 42}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	env, _ := proto.Read(&buf)
	if env.Type != proto.MsgHeartbeat {
		t.Fatalf("expected MsgHeartbeat, got %q", env.Type)
	}
	var hb proto.HeartbeatMsg
	proto.Decode(env, &hb)
	if hb.Seq != 42 {
		t.Errorf("Seq: want 42, got %d", hb.Seq)
	}
}

func TestWriteRead_Error(t *testing.T) {
	var buf bytes.Buffer
	proto.Write(&buf, proto.MsgError, proto.ErrorMsg{Message: "invalid token"})
	env, _ := proto.Read(&buf)
	if env.Type != proto.MsgError {
		t.Fatalf("expected MsgError, got %q", env.Type)
	}
	var e proto.ErrorMsg
	proto.Decode(env, &e)
	if e.Message != "invalid token" {
		t.Errorf("Message: want %q, got %q", "invalid token", e.Message)
	}
}

// --- Multiple messages on the same stream ---

func TestWriteRead_MultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	proto.Write(&buf, proto.MsgHello, proto.HelloMsg{Token: "t", LocalPort: 80})
	proto.Write(&buf, proto.MsgHeartbeat, proto.HeartbeatMsg{Seq: 1})
	proto.Write(&buf, proto.MsgHeartbeat, proto.HeartbeatMsg{Seq: 2})

	types := []proto.MsgType{proto.MsgHello, proto.MsgHeartbeat, proto.MsgHeartbeat}
	for _, want := range types {
		env, err := proto.Read(&buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if env.Type != want {
			t.Errorf("want %q, got %q", want, env.Type)
		}
	}
}

// --- Wire format: must be a single newline-terminated JSON line ---

func TestWrite_ProducesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	proto.Write(&buf, proto.MsgHello, proto.HelloMsg{Token: "x", LocalPort: 1})

	line := buf.String()
	newlines := strings.Count(line, "\n")
	if newlines != 1 {
		t.Errorf("expected exactly 1 newline, got %d in %q", newlines, line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("line must end with newline")
	}
}

// --- Error cases ---

func TestRead_EOF(t *testing.T) {
	_, err := proto.Read(strings.NewReader(""))
	if err == nil {
		t.Error("expected error on empty reader, got nil")
	}
}

func TestRead_InvalidJSON(t *testing.T) {
	_, err := proto.Read(strings.NewReader("not-json\n"))
	if err == nil {
		t.Error("expected error on invalid JSON")
	}
}

func TestWrite_WriterError(t *testing.T) {
	err := proto.Write(errWriter{}, proto.MsgHello, proto.HelloMsg{})
	if err == nil {
		t.Error("expected error when writer fails")
	}
}

func TestDecode_InvalidTarget(t *testing.T) {
	var buf bytes.Buffer
	proto.Write(&buf, proto.MsgHello, proto.HelloMsg{Token: "t", LocalPort: 1})
	env, _ := proto.Read(&buf)

	// Decode into a type that doesn't match (string vs struct) — should error
	var bad int
	if err := proto.Decode(env, &bad); err == nil {
		t.Error("expected Decode error on type mismatch, got nil")
	}
}

// errWriter always returns an error on Write.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, io.ErrClosedPipe
}