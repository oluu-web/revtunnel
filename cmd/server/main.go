package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
	"github.com/oluu-web/lennut/internal/api"
	"github.com/oluu-web/lennut/internal/auth"
	dbpkg "github.com/oluu-web/lennut/internal/db"
	"github.com/oluu-web/lennut/internal/proto"
	"github.com/oluu-web/lennut/internal/registry"
	"github.com/oluu-web/lennut/internal/relay"
	"github.com/oluu-web/lennut/internal/utils"
)

func main() {
	tunnelAddr := flag.String("tunnel-addr", ":4443", "address agents connect to")
	httpAddr   := flag.String("http-addr", ":8080", "address for public HTTP traffic")
	domain     := flag.String("domain", "localhost", "base domain for hostnames")
	certFile   := flag.String("cert", "server.crt", "TLS certificate file")
	keyFile    := flag.String("key", "server.key", "TLS key file")
	token      := flag.String("token", "secret123", "shared API token")
	databaseURL := flag.String("database-url", "", "PostgreSQL connection string")
	tokenSigningSecret := flag.String("token-signing-secret", "", "secret used to sign auth tokens")
	flag.Parse()

	var db *sql.DB
	if *databaseURL != "" {
		var err error
		db, err = dbpkg.Open(*databaseURL)
		if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("database connected")
	}
	_ = db

	issuer, err := auth.NewTokenIssuer(
		*tokenSigningSecret,
		"revtunnel",
		"revtunnel-agent",
		time.Hour,
	)
	if err != nil {
		slog.Error("init token issuer", "err", err)
		os.Exit(1)
	}

	tunnelHandler := api.TunnelHandler{
		DB: db,
		Domain: *domain,
	}

	reg := registry.New()
	authHandler := &api.AuthHandler{
		DB: db,
		Tokens: issuer,
	}
	requireJWT := api.RequireJWT(issuer)

	mux := http.NewServeMux()
	mux.Handle("/auth/token", authHandler)
	mux.Handle("/me", requireJWT(http.HandlerFunc(api.MeHandler)))
	mux.Handle("/tunnels", requireJWT(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			tunnelHandler.ListTunnels(w, r)
		case http.MethodPost:
			tunnelHandler.CreateTunnel(w, r)
		default:
			utils.WriteJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})))

mux.Handle("/tunnels/", requireJWT(http.HandlerFunc(tunnelHandler.DeleteTunnel)))
	mux.Handle("/", &relay.Handler{Registry: reg})

	go func() {
		for range time.Tick(90 * time.Second) {
			reg.Reap(120 * time.Second)
		}
	}()

	go serveTunnel(db, *tunnelAddr, *certFile, *keyFile, *token, *domain, reg)

	slog.Info("HTTP listener ready", "addr", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		slog.Error("HTTP server", "err", err)
		os.Exit(1)
	}
}

// Starts the TLS listener the agents connect to

func serveTunnel(db *sql.DB, addr, certFile, keyFile, token, domain string, reg *registry.Registry) {
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
		go handleAgent(conn, db, token, domain, reg)
	}
}

func handleAgent(conn net.Conn, db *sql.DB, token, domain string, reg *registry.Registry) {
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

	ctrlReader := bufio.NewReader(ctrl)

	env, err := proto.Read(ctrlReader)
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
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: "invalid token"})
		slog.Warn("rejected agent: bad token", "remote", conn.RemoteAddr())
		return
	}

	if err := hello.Validate(); err != nil {
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: err.Error()})
		slog.Warn("rejected agent: invalid HELLO", "err", err, "remote", conn.RemoteAddr())
		return
	}

	hostname, err := activateTunnel(context.Background(), db, hello.TunnelID)
	if err != nil {
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: "failed to activate tunnel"})
		slog.Error("activate tunnel", "tunnel_id", hello.TunnelID, "err", err)
		return
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := markTunnelClosed(ctx, db, hello.TunnelID); err != nil {
			slog.Error("mark tunnel closed", "tunnel_id", hello.TunnelID, "err", err)
		}
	}()

	sessionID := uuid.NewString()
	now := time.Now()

	entry := &registry.Entry{
		SessionID: sessionID,
		Hostname:  hostname,
		Session:   mux,
		CreatedAt: now,
		LastSeen:  now,
	}

	if err := reg.Add(entry); err != nil {
		slog.Error("registry add", "err", err)
		_ = proto.Write(ctrl, proto.MsgError, proto.ErrorMsg{Message: err.Error()})
		return
	}
	defer reg.Remove(sessionID)

	if err := proto.Write(ctrl, proto.MsgHelloAck, proto.HelloAckMsg{
		SessionID: sessionID,
		Hostname:  hostname,
	}); err != nil {
		slog.Error("write HELLO_ACK", "err", err)
		return
	}

	slog.Info("tunnel up", "hostname", hostname, "session", sessionID, "tunnel_id", hello.TunnelID)

	var seq int64
	for {
		env, err := proto.Read(ctrlReader)
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

func activateTunnel(ctx context.Context, db *sql.DB, tunnelID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	row := db.QueryRowContext(
		ctx,
		`
		UPDATE tunnels
		SET status = 'active'
		WHERE id = $1
		  AND status = 'pending'
		RETURNING hostname
		`,
		tunnelID,
	)

	var hostname string
	if err := row.Scan(&hostname); err != nil {
		return "", err
	}

	return hostname, nil
}

func markTunnelClosed(ctx context.Context, db *sql.DB, tunnelID string) error {
	_, err := db.ExecContext(
		ctx,
		`
		UPDATE tunnels
		SET status = 'closed',
		    closed_at = now()
		WHERE id = $1
		  AND status IN ('pending', 'active')
		`,
		tunnelID,
	)
	return err
}