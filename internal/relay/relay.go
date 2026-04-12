package relay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/oluu-web/lennut/internal/registry"
)

type Handler struct {
	Registry *registry.Registry
}

type agentTransport struct {
	entry *registry.Entry
}

func (t *agentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	stream, err := t.entry.Session.Open()
	if err != nil {
		return nil, fmt.Errorf("relay: open yamux stream: %w", err)
	}

	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return stream, nil
		},
		DisableKeepAlives: true,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		stream.Close()
		return nil, fmt.Errorf("relay: round trip: %w", err)
	}

	return resp, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r*http.Request) {
	hostname := r.Host
	if idx := strings.LastIndex(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}

	entry, ok := h.Registry.Get(hostname)
	if !ok {
		http.Error(w, "no tunnel found for this hostname", http.StatusNotFound)
		return
	}

	if entry.Session.IsClosed() {
		h.Registry.Remove(entry.SessionID)
		http.Error(w, "tunnel sessin is closed", http.StatusBadGateway)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func (req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = hostname
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Forwarded-or", r.RemoteAddr)
		},
		Transport:  &agentTransport{entry: entry},
		ErrorHandler: func (w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy error", "hostname", hostname, "err", err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}