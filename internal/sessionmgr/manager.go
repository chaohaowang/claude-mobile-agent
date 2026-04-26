// Package sessionmgr owns the live set of session watchers on a Mac.
// One watcher per session_id (cwd absolute path). The Daemon drives it
// via Sync() each time the tmux scanner reports a fresh desired set.
package sessionmgr

import "sync"

// Watcher is the minimum a session implementation must expose. The real
// implementation in package daemon adds jsonl tailing, status polling,
// and a tmux send-keys target.
type Watcher interface {
	SessionID() string
	Stop()
}

// Registry maps session_id → live Watcher.
type Registry struct {
	mu       sync.Mutex
	watchers map[string]Watcher
}

func NewRegistry() *Registry {
	return &Registry{watchers: make(map[string]Watcher)}
}

// Sync reconciles the registry against `desired`. Watchers in the
// registry but not in desired are stopped and removed. New ids in
// desired call `spawn(id)` to mint a fresh Watcher. Returns the
// added/removed id lists for the caller to broadcast as session.list.
func (r *Registry) Sync(desired []string, spawn func(id string) Watcher) (added, removed []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	for id, w := range r.watchers {
		if _, ok := desiredSet[id]; !ok {
			w.Stop()
			delete(r.watchers, id)
			removed = append(removed, id)
		}
	}
	for id := range desiredSet {
		if _, ok := r.watchers[id]; ok {
			continue
		}
		r.watchers[id] = spawn(id)
		added = append(added, id)
	}
	return added, removed
}

// Get returns the watcher for a session_id, or nil if none.
func (r *Registry) Get(id string) Watcher {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watchers[id]
}

// IDs returns the current session_id set, in unspecified order.
func (r *Registry) IDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.watchers))
	for id := range r.watchers {
		ids = append(ids, id)
	}
	return ids
}

// StopAll stops and forgets every watcher. Used during daemon shutdown.
func (r *Registry) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, w := range r.watchers {
		w.Stop()
		delete(r.watchers, id)
	}
}
