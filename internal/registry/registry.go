package registry

import (
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type Entry struct {
	SessionID string
	Hostname string
	Session *yamux.Session
	CreatedAt time.Time
	LastSeen time.Time
}

type Registry struct {
	mu sync.RWMutex
	byHost map[string]*Entry
	byID map[string]*Entry
}

func New() *Registry {
	return &Registry{
		byHost: make(map[string]*Entry),
		byID: make(map[string]*Entry),
	}
}

func (r *Registry) Add(e *Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if exsisting, ok := r.byHost[e.Hostname]; ok {
		if !exsisting.Session.IsClosed() {
			return fmt.Errorf("registry: hostname %q aready actiev", e.Hostname)
		}

		delete(r.byID, exsisting.SessionID)
	}

	r.byHost[e.Hostname] = e
	r.byID[e.SessionID] = e
	return nil
}

func (r *Registry) Get(hostname string) (*Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byHost[hostname]
	return  e, ok
}

func (r *Registry) Remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[sessionID]
	if !ok {
		return
	}
	delete(r.byHost, e.Hostname)
	delete(r.byID, sessionID)
}

func (r *Registry) Touch(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.byID[sessionID]; ok {
		e.LastSeen = time.Now()
	}
}

func (r *Registry) Reap(ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-ttl)
	for id, e := range r.byID {
		if e.LastSeen.Before(cutoff) || e.Session.IsClosed() {
			delete(r.byHost, e.Hostname)
			delete(r.byID, id)
		}
	}
}

func (r *Registry) List() []*Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]*Entry, 0, len(r.byID))
	for _, e := range r.byID {
		entries = append(entries, e)
	}

	return entries
}