/*
Control-Channel framing proocol between the agent and the edge server.
Messages are dewline-delimited JSON sent over a yamux stream.
*/
package proto

import (
	"encoding/json"
	"fmt"
	"io"
)

type MsgType string

const (
	MsgHello MsgType = "HELLO"
	MsgHelloAck MsgType = "HELLO_ACK"
	MsgHeartbeat MsgType = "HEARTBEAT"
	MsgError MsgType = "ERROR"
)

// Envelopoe wraps the messgae so the reader can know what struct to decode into

type Envelope struct {
	Type MsgType `json:"type"`
	Data json.RawMessage `json:"data"`
}

// HelloMsg is the first thing the agent sends after connecting
type HelloMsg struct {
	Token string `json:"token"`//authentication for the agent
	LocalPort int `json:"local_port"` //port to expose
}

// HelloAckMsg is the serves's confirmation that the tunnel is live
type HelloAckMsg struct {
	SessionID string `json:"session_id"` // unique ID. for the tunnel session
	Hostname string `json:"hostname"` // public hostname
}

type ErrorMsg struct {
	Message string `json:"message"`
}

// HeartbeatMsg is just a ping
type HeartbeatMsg struct {
	Seq int64 `json:"seq"`
}

/* 
	Write encode payload as JSON, wraps it in an Envelope ad writes it as a 
	single newline0terminated line to the writer
*/

func Write(w io.Writer, msgType MsgType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("proto: marshal payload %w", err)
	}

	env := Envelope{Type: msgType, Data: data}
	line, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("proto: marshal envelope: %w", err)
	}

	_, err = fmt.Fprintf(w, "%s\n", line)
	return err
}

/*
	Read reads one JSON envelope from r.
 */

	func Read(r io.Reader) (Envelope, error) {
		var line []byte
		buf := make([]byte, 1)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				line = append(line, buf[0])
				if buf[0] == '\n' {
					break
				}
			}
			if err != nil {
				return Envelope{}, fmt.Errorf("proto: read: %w", err)
			}
		}

		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return Envelope{}, fmt.Errorf("proto: unmarshal: %w", err)
		}

		return env, nil
	}

	/*
		Decode unpacks the datat filed on an envelope into a target.
		Thos should be called once the message type is known
	*/

	func Decode(env Envelope, target any) error {
		return json.Unmarshal(env.Data, target)
	}