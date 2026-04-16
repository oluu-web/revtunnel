package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
	"github.com/oluu-web/lennut/internal/proto"
	"github.com/oluu-web/lennut/internal/registry"
	"github.com/oluu-web/lennut/internal/relay"
)

func main() {
	tunnelAddr := flag.String("tunnel-addr", ":4443", "address agents connect to")
	httpAddr   := flag.String("http-addr", ":8080", "address for public HTTP traffic")
	domain     := flag.String("domain", "localhost", "base domain for hostnames")
	certFile   := flag.String("cert", "server.crt", "TLS certificate file")
	keyFile    := flag.String("key", "server.key", "TLS key file")
	token      := flag.String("token", "secret123", "shared API token")
	flag.Parse()

	reg := registry.New()

	go func() {
		for range time.Tick(90 * time.Second) {
			reg.Reap(120 * time.Second)
		}
	}()

	go serveTunnel(*tunnelAddr, *certFile, *keyFile, *token, *domain, reg)

	slog.Info("HTTP listener ready", "addr", *httpAddr)
	h := &relay.Handler{Registry: reg}
	if err := http.ListenAndServe(*httpAddr, h); err != nil {
		slog.Error("HTTP server", "err", err)
		os.Exit(1)
	}
}

// Starts the TLS listener the agents connect to

func serveTunnel(addr, certFile, keyFile, token, domain string, reg *registry.Registry) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		slog.Error("load TLS cert", "err", err)
		os.Exit(1)
	}

	ln, err := tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		slog.Error("tunnel listen", "err", err)
		os.Exit(1)
	}

	slog.Info("tunnel listener ready", "addr", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("tunnel accept", "err", err)
			continue
		}
		go handleAgent(conn, token, domain, reg)
	}
}

func handleAgent(conn net.Conn, token, domain string, reg *registry.Registry) {
	defer conn.Close()

	mux, err := yamux.Server(conn, yamux.DefaultConfig())
	if err != nil {
		slog.Error("yamux server", "err", err)
		return
	}
	defer mux.Close()

	ctrl, err := mux.Accept()
	if err != nil {
		slog.Error("accept control stream", "err", err)
		return
	}
	defer ctrl.Close()

	// Handshake

	env, err := proto.Read(ctrl)
	if err != nil || env.Type != proto.MsgHello {
		slog.Warn("expected HELLO", "got", env.Type, "err", err)
		return
	}

	var hello proto.HelloMsg
	if err := proto.Decode(env, &hello); err != nil {
		slog.Warn("decode HELLO", "err", err)
		return
	}

	if hello.Token != token {
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: "Invalid token"})
		slog.Warn("rejected agent: bad token", "remote", conn.RemoteAddr())
		return
	}

	sessionID := uuid.NewString()
	hostname := fmt.Sprintf("%s.%s", randomHex(8), domain)
	now := time.Now()

	entry := &registry.Entry{
		SessionID: sessionID,
		Hostname:  hostname,
		Session:   mux,
		CreatedAt: now,
		LastSeen:  now,
	}

	if err := reg.Add(entry); err != nil {
		slog.Error("regsgtry add", "err", err)
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: err.Error()})
		return
	}
	defer reg.Remove(sessionID)

	if err := proto.Write(ctrl, proto.MsgHelloAck, proto.HelloAckMsg{
		SessionID: sessionID,
		Hostname: hostname,
	}); err != nil {
		slog.Error("write HELLO_ACK", "err", err)
		return
	}
	slog.Info("tunnel up", "hostname", hostname, "session", sessionID)

	var seq int64
	for {
		env, err := proto.Read(ctrl)
		if err != nil {
			slog.Info("tunnel down", "hostname", hostname, "err", err)
			return
		}
		if env.Type == proto.MsgHeartbeat {
			seq++
			reg.Touch(sessionID)
			_ = proto.Write(ctrl, proto.MsgHeartbeat, proto.HeartbeatMsg{Seq: seq})
		}
	}
}

func randomHex(n int) string {
	const chars = "abcdef0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}