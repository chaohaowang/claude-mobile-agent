package daemon

import (
	"testing"
	"time"

	"github.com/chaohaow/claude-mobile-agent/internal/wire"
)

// fakeWatcher captures the calls the dispatcher routes to it.
type fakeWatcher struct {
	id           string
	historyCalls []wire.SessionHistoryReq
	sendCalls    []wire.SessionSend
	asrCalls     []wire.ASRRequest
	stopCalls    int
}

func (f *fakeWatcher) SessionID() string                       { return f.id }
func (f *fakeWatcher) Stop()                                   { f.stopCalls++ }
func (f *fakeWatcher) HandleHistory(req wire.SessionHistoryReq) { f.historyCalls = append(f.historyCalls, req) }
func (f *fakeWatcher) HandleSend(req wire.SessionSend)          { f.sendCalls = append(f.sendCalls, req) }
func (f *fakeWatcher) HandleASR(req wire.ASRRequest)            { f.asrCalls = append(f.asrCalls, req) }

func TestDispatch_HistoryReq_RoutesToWatcher(t *testing.T) {
	d := newTestDaemon(t)
	fw := &fakeWatcher{id: "/tmp/proj-a"}
	d.installWatcher(fw)

	d.dispatch(wire.Frame{
		Type:    wire.FrameTypeSessionHistoryReq,
		Payload: wire.SessionHistoryReq{SessionID: "/tmp/proj-a", Last: 50},
	})

	if len(fw.historyCalls) != 1 {
		t.Fatalf("history not routed: %+v", fw.historyCalls)
	}
	if fw.historyCalls[0].Last != 50 {
		t.Fatalf("payload lost: %+v", fw.historyCalls[0])
	}
}

func TestDispatch_HistoryReq_UnknownSession_NoOp(t *testing.T) {
	d := newTestDaemon(t)
	fw := &fakeWatcher{id: "/tmp/proj-a"}
	d.installWatcher(fw)

	d.dispatch(wire.Frame{
		Type:    wire.FrameTypeSessionHistoryReq,
		Payload: wire.SessionHistoryReq{SessionID: "/tmp/missing", Last: 50},
	})

	if len(fw.historyCalls) != 0 {
		t.Fatalf("unknown session was routed")
	}
}

func TestDispatch_SessionListReq_EmitsList(t *testing.T) {
	d := newTestDaemon(t)
	d.installWatcher(&fakeWatcher{id: "/tmp/a"})
	d.installWatcher(&fakeWatcher{id: "/tmp/b"})

	d.dispatch(wire.Frame{Type: wire.FrameTypeSessionListReq, Payload: wire.SessionListReq{}})

	select {
	case f := <-d.outbound:
		if f.Type != wire.FrameTypeSessionList {
			t.Fatalf("got type %s", f.Type)
		}
		list, ok := f.Payload.(wire.SessionList)
		if !ok {
			t.Fatalf("payload type %T", f.Payload)
		}
		if len(list.Sessions) != 2 {
			t.Fatalf("want 2 sessions, got %d", len(list.Sessions))
		}
	case <-time.After(time.Second):
		t.Fatal("no frame emitted")
	}
}

func TestDispatch_SessionSend_RoutesToWatcher(t *testing.T) {
	d := newTestDaemon(t)
	fw := &fakeWatcher{id: "/tmp/proj-a"}
	d.installWatcher(fw)

	d.dispatch(wire.Frame{
		Type:    wire.FrameTypeSessionSend,
		Payload: wire.SessionSend{SessionID: "/tmp/proj-a", Text: "hello"},
	})

	if len(fw.sendCalls) != 1 || fw.sendCalls[0].Text != "hello" {
		t.Fatalf("send not routed: %+v", fw.sendCalls)
	}
}

// helper to build a daemon with a small outbound buffer and a registry-bypass
// installWatcher so tests can inject fakes directly.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	return &Daemon{
		registry: nil, // use installWatcher path; getWatcher falls back when registry is nil
		outbound: make(chan wire.Frame, 8),
		sessions: make(map[string]sessionWatcher),
	}
}

func (d *Daemon) installWatcher(fw sessionWatcher) {
	d.sessions[fw.SessionID()] = fw
}
