package sessionmgr

import (
	"sort"
	"testing"
)

// fakeWatcher implements the minimum interface the Registry needs.
type fakeWatcher struct {
	id      string
	stopped bool
}

func (f *fakeWatcher) SessionID() string { return f.id }
func (f *fakeWatcher) Stop()             { f.stopped = true }

func TestRegistry_Sync_AddRemove(t *testing.T) {
	r := NewRegistry()

	spawn := func(id string) Watcher { return &fakeWatcher{id: id} }

	// Empty → {a, b}: both added.
	added, removed := r.Sync([]string{"a", "b"}, spawn)
	sort.Strings(added)
	if want := []string{"a", "b"}; !equalStrings(added, want) {
		t.Fatalf("added = %v, want %v", added, want)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v", removed)
	}

	// {a, b} → {b, c}: a removed (stopped), c added.
	added, removed = r.Sync([]string{"b", "c"}, spawn)
	if want := []string{"c"}; !equalStrings(added, want) {
		t.Fatalf("added2 = %v", added)
	}
	if want := []string{"a"}; !equalStrings(removed, want) {
		t.Fatalf("removed2 = %v", removed)
	}
	if w := r.Get("a"); w != nil {
		t.Fatalf("watcher a should be gone, got %+v", w)
	}

	// {b, c} → {b, c}: idempotent.
	added, removed = r.Sync([]string{"b", "c"}, spawn)
	if len(added)+len(removed) != 0 {
		t.Fatalf("idempotent sync produced deltas: +%v -%v", added, removed)
	}
}

func TestRegistry_Sync_StopsRemoved(t *testing.T) {
	r := NewRegistry()
	var lastSpawn *fakeWatcher
	spawn := func(id string) Watcher {
		fw := &fakeWatcher{id: id}
		if id == "a" {
			lastSpawn = fw
		}
		return fw
	}
	r.Sync([]string{"a", "b"}, spawn)
	if lastSpawn == nil {
		t.Fatal("a was never spawned")
	}
	r.Sync([]string{"b"}, spawn)
	if !lastSpawn.stopped {
		t.Fatal("removed watcher's Stop() was not called")
	}
}

func TestRegistry_IDs(t *testing.T) {
	r := NewRegistry()
	spawn := func(id string) Watcher { return &fakeWatcher{id: id} }
	r.Sync([]string{"foo", "bar", "baz"}, spawn)
	ids := r.IDs()
	sort.Strings(ids)
	if want := []string{"bar", "baz", "foo"}; !equalStrings(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
}

func TestRegistry_StopAll(t *testing.T) {
	r := NewRegistry()
	var watchers []*fakeWatcher
	spawn := func(id string) Watcher {
		fw := &fakeWatcher{id: id}
		watchers = append(watchers, fw)
		return fw
	}
	r.Sync([]string{"a", "b", "c"}, spawn)
	r.StopAll()
	for _, w := range watchers {
		if !w.stopped {
			t.Fatalf("watcher %q not stopped after StopAll", w.id)
		}
	}
	if len(r.IDs()) != 0 {
		t.Fatal("IDs not empty after StopAll")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
