package relay_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/oluu-web/lennut/internal/registry"
	"github.com/oluu-web/lennut/internal/relay"
)

type singleConnListener struct {
	conn net.Conn
	once bool
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.once {
		select {}
	}
	l.once = true
	return l.conn, nil
}
func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

func wireSession(t *testing.T, backend http.Handler) (*yamux.Session, func()) {
	t.Helper()
	edgeConn, agentConn := net.Pipe()

	edgeSess, err := yamux.Server(edgeConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux.Server: %v", err)
	}
	agentSess, err := yamux.Client(agentConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}

	go func() {
		for {
			stream, err := agentSess.Accept()
			if err != nil {
				return
			}
			go func(s net.Conn) {
				defer s.Close()
				srv := &http.Server{Handler: backend}
				srv.Serve(&singleConnListener{conn: s})
			}(stream)
		}
	}()

	cancel := func() {
		edgeSess.Close()
		agentSess.Close()
		edgeConn.Close()
		agentConn.Close()
	}
	return edgeSess, cancel
}

func regWith(t *testing.T, hostname string, sess *yamux.Session) *registry.Registry {
	t.Helper()
	reg := registry.New()
	now := time.Now()
	if err := reg.Add(&registry.Entry{
		SessionID: "test-session",
		Hostname:  hostname,
		Session:   sess,
		CreatedAt: now,
		LastSeen:  now,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	return reg
}

func TestServeHTTP_ProxiesToBackend(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from backend")
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "abc.revtunnel.xyz", sess)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "abc.revtunnel.xyz"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hello from backend") {
		t.Errorf("unexpected body: %q", rr.Body.String())
	}
}

func TestServeHTTP_StripsPortFromHost(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "abc.revtunnel.xyz", sess)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "abc.revtunnel.xyz:8080"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("port stripping failed — want 200, got %d", rr.Code)
	}
}

func TestServeHTTP_SetsXForwardedHost(t *testing.T) {
	var gotHost string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Header.Get("X-Forwarded-Host")
		w.WriteHeader(http.StatusOK)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "abc.revtunnel.xyz", sess)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "abc.revtunnel.xyz"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotHost != "abc.revtunnel.xyz" {
		t.Errorf("X-Forwarded-Host: want abc.revtunnel.xyz, got %q", gotHost)
	}
}

func TestServeHTTP_XForwardedFor_Typo(t *testing.T) {
	var gotFor, gotOr string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFor = r.Header.Get("X-Forwarded-For")
		gotOr = r.Header.Get("X-Forwarded-or")
		w.WriteHeader(http.StatusOK)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "abc.revtunnel.xyz", sess)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "abc.revtunnel.xyz"
	req.RemoteAddr = "1.2.3.4:9999"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(gotFor, "1.2.3.4") {
		t.Errorf(
			"BUG: X-Forwarded-For is empty (got %q); "+
				"relay.go sets \"X-Forwarded-or\" (typo) instead — got X-Forwarded-or=%q. "+
				"Fix: change \"X-Forwarded-or\" to \"X-Forwarded-For\" in relay.go Director",
			gotFor, gotOr,
		)
	}
}

func TestServeHTTP_UnknownHostname_Returns404(t *testing.T) {
	h := &relay.Handler{Registry: registry.New()}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "ghost.revtunnel.xyz"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rr.Code)
	}
}

func TestServeHTTP_ClosedSession_Returns502(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	sess, cancel := wireSession(t, backend)
	cancel()

	time.Sleep(30 * time.Millisecond)

	h := &relay.Handler{Registry: regWith(t, "dead.revtunnel.xyz", sess)}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "dead.revtunnel.xyz"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("want 502, got %d", rr.Code)
	}
}

func TestServeHTTP_ClosedSession_RemovedFromRegistry(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	sess, cancel := wireSession(t, backend)
	reg := regWith(t, "cleanup.revtunnel.xyz", sess)
	cancel()
	time.Sleep(30 * time.Millisecond)

	h := &relay.Handler{Registry: reg}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "cleanup.revtunnel.xyz"
	h.ServeHTTP(httptest.NewRecorder(), req)

	_, ok := reg.Get("cleanup.revtunnel.xyz")
	if ok {
		t.Error("closed session should be removed from registry after serving 502")
	}
}
func TestServeHTTP_PassthroughStatusCode(t *testing.T) {
	for _, code := range []int{201, 204, 400, 404, 500} {
		code := code
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			})
			sess, cancel := wireSession(t, backend)
			defer cancel()

			h := &relay.Handler{Registry: regWith(t, "status.revtunnel.xyz", sess)}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "status.revtunnel.xyz"
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != code {
				t.Errorf("want %d, got %d", code, rr.Code)
			}
		})
	}
}

func TestServeHTTP_PassthroughResponseBody(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "body.revtunnel.xyz", sess)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "body.revtunnel.xyz"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
	if body := rr.Body.String(); body != `{"status":"ok"}` {
		t.Errorf("body mismatch: %q", body)
	}
}

func TestServeHTTP_PassthroughRequestBody(t *testing.T) {
	var received string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.WriteHeader(http.StatusOK)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "req.revtunnel.xyz", sess)}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("payload=hello"))
	req.Host = "req.revtunnel.xyz"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if received != "payload=hello" {
		t.Errorf("backend received %q, want %q", received, "payload=hello")
	}
}
func TestServeHTTP_ConcurrentRequests(t *testing.T) {
	var mu sync.Mutex
	count := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	sess, cancel := wireSession(t, backend)
	defer cancel()

	h := &relay.Handler{Registry: regWith(t, "concurrent.revtunnel.xyz", sess)}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "concurrent.revtunnel.xyz"
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("concurrent: want 200, got %d", rr.Code)
			}
		}()
	}
	wg.Wait()

	if count != 10 {
		t.Errorf("backend hit count: want 10, got %d", count)
	}
}
