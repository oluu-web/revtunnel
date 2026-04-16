package registry_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/oluu-web/lennut/internal/registry"
)

func newSession(t *testing.T) (*yamux.Session, func()) {
	t.Helper()
	client, server := net.Pipe()

	go func() {
		yamux.Server(server, yamux.DefaultConfig()) //nolint:errcheck
	}()

	sess, err := yamux.Client(client, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	return sess, func() {
		sess.Close()
		client.Close()
		server.Close()
	}
}

func entry(sessionID, hostname string, sess *yamux.Session) *registry.Entry {
	now := time.Now()
	return &registry.Entry{
		SessionID: sessionID,
		Hostname:  hostname,
		Session:   sess,
		CreatedAt: now,
		LastSeen:  now,
	}
}

func TestAdd_And_Get(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	if err := reg.Add(entry("s1", "abc.revtunnel.xyz", sess)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := reg.Get("abc.revtunnel.xyz")
	if !ok {
		t.Fatal("Get: expected hit, got miss")
	}
	if got.SessionID != "s1" {
		t.Errorf("SessionID: want s1, got %q", got.SessionID)
	}
}

func TestGet_Miss(t *testing.T) {
	reg := registry.New()
	_, ok := reg.Get("ghost.revtunnel.xyz")
	if ok {
		t.Error("Get on empty registry should return false")
	}
}


func TestAdd_DuplicateHostname_LiveSession_Errors(t *testing.T) {
	reg := registry.New()
	sess1, cancel1 := newSession(t)
	defer cancel1()
	sess2, cancel2 := newSession(t)
	defer cancel2()

	reg.Add(entry("s1", "dup.revtunnel.xyz", sess1))

	err := reg.Add(entry("s2", "dup.revtunnel.xyz", sess2))
	if err == nil {
		t.Error("expected error adding duplicate live hostname, got nil")
	}
}

func TestAdd_DuplicateHostname_StaleSession_Replaces(t *testing.T) {
	reg := registry.New()
	sess1, cancel1 := newSession(t)
	cancel1() // close it immediately so IsClosed() == true

	reg.Add(entry("s1", "dup.revtunnel.xyz", sess1))

	sess2, cancel2 := newSession(t)
	defer cancel2()

	if err := reg.Add(entry("s2", "dup.revtunnel.xyz", sess2)); err != nil {
		t.Errorf("Add over stale session should succeed, got: %v", err)
	}
	got, _ := reg.Get("dup.revtunnel.xyz")
	if got.SessionID != "s2" {
		t.Errorf("expected s2 after stale replacement, got %q", got.SessionID)
	}
}

func TestRemove_DeletesEntry(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	reg.Add(entry("s1", "rem.revtunnel.xyz", sess))
	reg.Remove("s1")

	_, ok := reg.Get("rem.revtunnel.xyz")
	if ok {
		t.Error("entry should be gone after Remove")
	}
}

func TestRemove_NonExistent_NoOp(t *testing.T) {
	reg := registry.New()
	reg.Remove("ghost-session-id")
}

func TestRemove_RemovesFromBothIndexes(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	reg.Add(entry("s1", "both.revtunnel.xyz", sess))
	reg.Remove("s1")

	if _, ok := reg.Get("both.revtunnel.xyz"); ok {
		t.Error("byHost entry should be removed")
	}
	// List should be empty too.
	if l := reg.List(); len(l) != 0 {
		t.Errorf("List: want 0, got %d", len(l))
	}
}

func TestTouch_UpdatesLastSeen(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	e := entry("s1", "touch.revtunnel.xyz", sess)
	e.LastSeen = time.Now().Add(-5 * time.Second)
	reg.Add(e)

	time.Sleep(5 * time.Millisecond)
	beforeTouch := time.Now()
	reg.Touch("s1")

	got, _ := reg.Get("touch.revtunnel.xyz")
	if !got.LastSeen.After(beforeTouch) {
		t.Error("LastSeen should be updated to after the Touch call")
	}
}

func TestTouch_NonExistent_NoOp(t *testing.T) {
	reg := registry.New()
	// Must not panic
	reg.Touch("ghost")
}

func TestReap_RemovesExpiredSessions(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	e := entry("s1", "expired.revtunnel.xyz", sess)
	e.LastSeen = time.Now().Add(-10 * time.Minute)
	reg.Add(e) 

	reg.Reap(1 * time.Minute)

	_, ok := reg.Get("expired.revtunnel.xyz")
	if ok {
		t.Error("Reap should have removed the expired session")
	}
}

func TestReap_KeepsLiveSessions(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	defer cancel()

	reg.Add(entry("s1", "live.revtunnel.xyz", sess))

	reg.Reap(1 * time.Minute)

	_, ok := reg.Get("live.revtunnel.xyz")
	if !ok {
		t.Error("Reap should not remove a recently-touched live session")
	}
}

func TestReap_RemovesClosedSessions(t *testing.T) {
	reg := registry.New()
	sess, cancel := newSession(t)
	reg.Add(entry("s1", "closed.revtunnel.xyz", sess))
	cancel()

	time.Sleep(20 * time.Millisecond) 
	reg.Reap(1 * time.Hour)

	_, ok := reg.Get("closed.revtunnel.xyz")
	if ok {
		t.Error("Reap should remove sessions whose yamux session is closed")
	}
}

func TestReap_EmptyRegistry_NoOp(t *testing.T) {
	reg := registry.New()
	// Must not panic on empty registry
	reg.Reap(1 * time.Minute)
}

func TestList_ReturnsAllEntries(t *testing.T) {
	reg := registry.New()
	for i, h := range []string{"a.x", "b.x", "c.x"} {
		sess, cancel := newSession(t)
		defer cancel()
		id := string(rune('a' + i))
		reg.Add(entry(id, h, sess))
	}
	if got := reg.List(); len(got) != 3 {
		t.Errorf("List: want 3, got %d", len(got))
	}
}

func TestList_EmptyRegistry(t *testing.T) {
	reg := registry.New()
	if got := reg.List(); len(got) != 0 {
		t.Errorf("List on empty registry: want 0, got %d", len(got))
	}
}

func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	reg := registry.New()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess, cancel := newSession(t)
			defer cancel()

			id := "sess-concurrent-" + string(rune('A'+i%26)) + string(rune('0'+i%10))
			host := id + ".revtunnel.xyz"
			e := entry(id, host, sess)

			reg.Add(e)
			reg.Get(host)
			reg.Touch(id)
			reg.List()
			reg.Remove(id)
		}(i)
	}
	wg.Wait()
}
