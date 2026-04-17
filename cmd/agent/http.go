// cmd/agent/http.go
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/oluu-web/lennut/internal/proto"
	"github.com/spf13/cobra"
)

var httpCmd = &cobra.Command{
	Use:   "http <port>",
	Short: "Expose a local HTTP port via a public tunnel",
	Long: `Connects to the tunnel server and exposes your local
HTTP service on a public hostname.

Example:
  tunnel http 3000
  tunnel http 8080 --server tunnel.yourdomain.io:4443`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, err := strconv.Atoi(args[0])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid port: %q", args[0])
		}
		if flagToken == "" {
			return fmt.Errorf(
				"not logged in — run `tunnel login --api-key <key>` first",
			)
		}
		return runHTTP(port)
	},
}

func init() {
	rootCmd.AddCommand(httpCmd)
}

func runHTTP(port int) error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	for {
		err := connect(ctx, port)
		select {
		case <-ctx.Done():
			fmt.Println("\ntunnel closed")
			return nil
		default:
			slog.Warn("disconnected, reconnecting in 5s", "err", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func connect(ctx context.Context, port int) error {
	tunnelID, hostname, err := registerTunnel(ctx, port)
	if err != nil {
		return fmt.Errorf("register tunnel: %w", err)
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: flagInsecure,
	}

	conn, err := tls.Dial("tcp", flagServer, tlsCfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", flagServer, err)
	}
	defer conn.Close()

	mux, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer mux.Close()

	ctrl, err := mux.Open()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	defer ctrl.Close()

	ctrlReader := bufio.NewReader(ctrl)

	if err := proto.Write(ctrl, proto.MsgHello, proto.HelloMsg{
		Token:    flagToken,
		TunnelID: tunnelID,
	}); err != nil {
		return fmt.Errorf("send HELLO: %w", err)
	}

	env, err := proto.Read(ctrlReader)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	switch env.Type {
	case proto.MsgError:
		var e proto.ErrorMsg
		_ = proto.Decode(env, &e)
		return fmt.Errorf("server rejected tunnel: %s", e.Message)
	case proto.MsgHelloAck:
		// expected — fall through to decode below
	default:
		return fmt.Errorf("unexpected message: %s", env.Type)
	}

	var ack proto.HelloAckMsg
	if err := proto.Decode(env, &ack); err != nil {
		return fmt.Errorf("decode HELLO_ACK: %w", err)
	}

	_ = hostname

	fmt.Printf("  Tunnel active\n")
	fmt.Printf("  Forwarding localhost:%d at https://%s", port, ack.Hostname)
	fmt.Printf("\n")
	fmt.Printf("  press Ctrl+C to stop\n\n")

	localAddr := fmt.Sprintf("localhost:%d", port)

	go acceptStreams(mux, localAddr)

	select {
	case <-ctx.Done():
		return nil
	case err := <-heartbeatDone(ctrl, ctrlReader):
		return err
	}
}

func registerTunnel(ctx context.Context, port int) (tunnelID, hostname string, err error) {
	httpBase := controlPlaneBase(flagServer)
	body, err := json.Marshal(map[string]any{"target_port": port})
	if err != nil {
		return "", "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		httpBase+"/tunnels",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+flagToken)

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: flagInsecure,
			},
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
		Error    string `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// success
	case http.StatusUnauthorized:
		slog.Debug("token expired, attempting silent refresh")
		if err := refreshToken(); err != nil {
			return "", "", err
		}
		return registerTunnel(ctx, port)
	case http.StatusConflict:
		return "", "", fmt.Errorf("tunnel limit reached — delete an existing tunnel first")
	case http.StatusUnprocessableEntity:
		return "", "", fmt.Errorf("a tunnel for port %d already exists", port)
	default:
		return "", "", fmt.Errorf("control plane error (%d): %s", resp.StatusCode, result.Error)
	}

	if result.ID == "" {
		return "", "", fmt.Errorf("control plane returned empty tunnel id")
	}

	return result.ID, result.Hostname, nil
}

func heartbeatDone(ctrl net.Conn, r *bufio.Reader) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- heartbeat(ctrl, r)
	}()
	return ch
}

func acceptStreams(mux *yamux.Session, localAddr string) {
	for {
		stream, err := mux.Accept()
		if err != nil {
			return
		}
		go proxyToLocal(stream, localAddr)
	}
}

func proxyToLocal(stream net.Conn, localAddr string) {
	defer stream.Close()

	local, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		slog.Error("dial local service", "addr", localAddr, "err", err)
		fmt.Fprintf(stream,
			"HTTP/1.1 502 Bad Gateway\r\n"+
				"Content-Length: 11\r\n"+
				"Connection: close\r\n\r\n"+
				"Bad Gateway")
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(local, stream)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(stream, local)
		done <- struct{}{}
	}()
	<-done
}

// heartbeat sends a ping every 30 seconds and waits for the server's echo.
// It takes the same ctrlReader used for the handshake so the bufio buffer is
// shared and no bytes are lost between the HELLO_ACK and the first heartbeat.
func heartbeat(ctrl net.Conn, r *bufio.Reader) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var seq int64
	for range ticker.C {
		seq++
		if err := proto.Write(ctrl, proto.MsgHeartbeat, proto.HeartbeatMsg{Seq: seq}); err != nil {
			return fmt.Errorf("heartbeat send: %w", err)
		}
		env, err := proto.Read(r)
		if err != nil {
			return fmt.Errorf("heartbeat recv: %w", err)
		}
		if env.Type != proto.MsgHeartbeat {
			slog.Warn("unexpected msg in heartbeat", "type", env.Type)
		}
	}
	return nil
}