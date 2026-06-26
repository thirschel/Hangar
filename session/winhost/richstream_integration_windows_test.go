//go:build windows

package winhost

// Hermetic integration tests for the rich-view event-stream pipe.
//
// Chosen seam: these tests drive (*host).runRichStream DIRECTLY over a real
// named pipe (winio Listen/Dial) rather than going through the full host RPC
// dispatch (OpenRichStream). runRichStream is the entire server side of the
// rich pipe -- token handshake, richSubscribe snapshot replay, live fan-out via
// emitFrame -> sub.ch, and client-disconnect teardown -- so calling it directly
// is the smallest seam that still exercises REAL framing over a REAL pipe. It
// also lets us build sdkSessions via newSDKSession WITHOUT injecting them into
// the host registry or triggering workspace revival, and without a live Copilot
// CLI: synthetic copilot.SessionEvent values are fed straight into onSDKEvent,
// exactly like the existing richstream unit tests. No production code changed.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/winhost/proto"

	"github.com/Microsoft/go-winio"
)

// richStreamTestSeq makes per-stream pipe names unique even when many streams
// are spun up in the same nanosecond (the concurrency test).
var richStreamTestSeq atomic.Uint64

// richCase is one synthetic event plus the frame fields it must serialize to.
type richCase struct {
	kind string
	text string
	ev   copilot.SessionEvent
}

// assistantMessage builds an assistant.message event carrying text.
func assistantMessage(text string) copilot.SessionEvent {
	return copilot.SessionEvent{Data: &copilot.AssistantMessageData{Content: text}}
}

// startRichStreamPipe creates a real named-pipe listener, an attach token, and
// runs (*host).runRichStream against sess in a goroutine. The returned done
// channel is closed when runRichStream returns, so tests can assert teardown.
func startRichStreamPipe(t *testing.T, h *host, sess *sdkSession, since uint64) (pipe, token string, done chan struct{}) {
	t.Helper()
	n := richStreamTestSeq.Add(1)
	pipe = fmt.Sprintf(`\\.\pipe\hangar-richtest-%d-%d-%d`, os.Getpid(), time.Now().UnixNano(), n)
	sddl, err := currentUserSDDL()
	if err != nil {
		t.Fatalf("sddl: %v", err)
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		t.Fatalf("listen pipe %q: %v", pipe, err)
	}
	token, err = randomNonceHex(16)
	if err != nil {
		_ = ln.Close()
		t.Fatalf("token: %v", err)
	}
	done = make(chan struct{})
	go func() {
		h.runRichStream(sess, ln, token, since)
		close(done)
	}()
	return pipe, token, done
}

// dialRichClient replicates the desktop client handshake (host-client.ts
// connectEventStream): dial the pipe, then write the attach token as a single
// length-prefixed frame. After this the server streams EventFrames.
func dialRichClient(t *testing.T, pipe, token string) net.Conn {
	t.Helper()
	to := 5 * time.Second
	conn, err := winio.DialPipe(pipe, &to)
	if err != nil {
		t.Fatalf("dial rich pipe %q: %v", pipe, err)
	}
	if err := proto.WriteRawFrame(conn, []byte(token)); err != nil {
		_ = conn.Close()
		t.Fatalf("write rich token: %v", err)
	}
	return conn
}

// readEventFrame reads one length-prefixed frame and decodes it as an
// EventFrame, bounded by an absolute read deadline so a bug fails fast instead
// of hanging the suite.
func readEventFrame(conn net.Conn, deadline time.Time) (proto.EventFrame, error) {
	_ = conn.SetReadDeadline(deadline)
	b, err := proto.ReadFrameBytes(conn)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return proto.EventFrame{}, err
	}
	var f proto.EventFrame
	if err := json.Unmarshal(b, &f); err != nil {
		return proto.EventFrame{}, fmt.Errorf("decode event frame: %w", err)
	}
	return f, nil
}

// TestRichStreamPipeEndToEnd (B): one session, one client over a real pipe.
// Asserts snapshot replay equals the injected events by Kind+Seq+payload in
// order, a post-connect live event is delivered as the next frame, and client
// disconnect tears the server goroutine down (no leak).
func TestRichStreamPipeEndToEnd(t *testing.T) {
	h := newHost(io.Discard, time.Minute)
	defer h.triggerShutdown()

	sess := newSDKSession(sdkSessionParams{name: "rich-e2e", program: "copilot", workDir: t.TempDir()}, nil, nil)
	defer sess.close()

	// Snapshot: inject distinct events BEFORE any client connects. These land in
	// the richLog replay buffer with seq 1..N.
	snapshot := []richCase{
		{proto.EventKindAssistantMessage, "alpha", assistantMessage("alpha")},
		{proto.EventKindAssistantDelta, "beta", copilot.SessionEvent{Data: &copilot.AssistantMessageDeltaData{DeltaContent: "beta"}}},
		{proto.EventKindReasoning, "gamma", copilot.SessionEvent{Data: &copilot.AssistantReasoningData{Content: "gamma"}}},
	}
	for _, c := range snapshot {
		sess.onSDKEvent(c.ev)
	}

	pipe, token, done := startRichStreamPipe(t, h, sess, 0)

	conn := dialRichClient(t, pipe, token)
	closed := false
	defer func() {
		if !closed {
			_ = conn.Close()
		}
	}()

	// Snapshot replay: frames equal the injected events by Kind+Seq+payload, in
	// the order they were injected.
	for i, c := range snapshot {
		f, err := readEventFrame(conn, time.Now().Add(5*time.Second))
		if err != nil {
			t.Fatalf("snapshot frame %d read: %v", i, err)
		}
		if f.Seq != uint64(i+1) {
			t.Fatalf("snapshot frame %d Seq = %d, want %d", i, f.Seq, i+1)
		}
		if f.Kind != c.kind || f.Text != c.text {
			t.Fatalf("snapshot frame %d = %+v, want kind=%q text=%q", i, f, c.kind, c.text)
		}
	}

	// Live event injected AFTER the client drained the snapshot (so the
	// subscription is registered) must arrive next with the next seq.
	wantSeq := uint64(len(snapshot) + 1)
	sess.onSDKEvent(assistantMessage("delta-live"))
	live, err := readEventFrame(conn, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("live frame read: %v", err)
	}
	if live.Seq != wantSeq || live.Kind != proto.EventKindAssistantMessage || live.Text != "delta-live" {
		t.Fatalf("live frame = %+v, want seq=%d kind=%q text=%q", live, wantSeq, proto.EventKindAssistantMessage, "delta-live")
	}

	// Client disconnect must drive runRichStream to return (its deferred
	// ln.Close()/conn.Close() run) -- i.e. no goroutine or pipe leak.
	closed = true
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runRichStream did not return after client disconnect (leak)")
	}
}

// richRig bundles one session's stream plumbing for the concurrency test.
type richRig struct {
	sess   *sdkSession
	conn   net.Conn
	done   chan struct{}
	marker string
}

// sessionMarker is the per-session text prefix; the trailing '|' keeps single-
// and multi-digit indices from colliding as substrings (e.g. S1| vs S10|).
func sessionMarker(i int) string { return fmt.Sprintf("S%d|", i) }

// assertOwnFrame checks a frame carries this client's marker and no other
// session's marker (cross-talk). Safe to call from goroutines: it only uses
// t.Helper/t.Errorf, never FailNow.
func assertOwnFrame(t *testing.T, i, n int, f proto.EventFrame) bool {
	t.Helper()
	own := sessionMarker(i)
	if !strings.HasPrefix(f.Text, own) {
		t.Errorf("client %d frame lacks own marker %q: %+v", i, own, f)
		return false
	}
	for k := 0; k < n; k++ {
		if k == i {
			continue
		}
		if strings.Contains(f.Text, sessionMarker(k)) {
			t.Errorf("client %d received foreign session %d frame: %+v", i, k, f)
			return false
		}
	}
	return true
}

// TestRichStreamConcurrentIsolation (C): N sessions, each fed DISTINCT events,
// with N clients reading concurrently over real pipes. Asserts each client sees
// ONLY its own session's frames (no cross-talk), per-session seq is strictly
// increasing, the whole fan-out completes within a deadline (no deadlock), and
// every server goroutine returns on disconnect.
func TestRichStreamConcurrentIsolation(t *testing.T) {
	const (
		sessions = 4
		perSnap  = 3
	)
	h := newHost(io.Discard, time.Minute)
	defer h.triggerShutdown()

	rigs := make([]richRig, sessions)
	for i := 0; i < sessions; i++ {
		s := newSDKSession(sdkSessionParams{name: fmt.Sprintf("rich-conc-%d", i), program: "copilot", workDir: t.TempDir()}, nil, nil)
		defer s.close()
		marker := sessionMarker(i)
		for j := 0; j < perSnap; j++ {
			s.onSDKEvent(assistantMessage(fmt.Sprintf("%smsg%d", marker, j)))
		}
		pipe, token, done := startRichStreamPipe(t, h, s, 0)
		rigs[i] = richRig{sess: s, conn: dialRichClient(t, pipe, token), done: done, marker: marker}
	}

	// A context deadline bounds every blocking read so a deadlock fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()

	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := rigs[i]
			var lastSeq uint64
			// Snapshot: exactly perSnap frames, all this session's, seq strictly
			// increasing.
			for j := 0; j < perSnap; j++ {
				f, err := readEventFrame(r.conn, deadline)
				if err != nil {
					t.Errorf("client %d snapshot read %d: %v", i, j, err)
					return
				}
				if !assertOwnFrame(t, i, sessions, f) {
					return
				}
				if f.Seq <= lastSeq {
					t.Errorf("client %d seq not strictly increasing: %d after %d", i, f.Seq, lastSeq)
					return
				}
				lastSeq = f.Seq
			}
			// Live: injected only after this client drained its snapshot, so the
			// subscription is registered and the frame arrives live (not in replay).
			r.sess.onSDKEvent(assistantMessage(r.marker + "live"))
			f, err := readEventFrame(r.conn, deadline)
			if err != nil {
				t.Errorf("client %d live read: %v", i, err)
				return
			}
			if !assertOwnFrame(t, i, sessions, f) {
				return
			}
			if f.Text != r.marker+"live" || f.Seq != uint64(perSnap+1) {
				t.Errorf("client %d live frame = %+v, want text=%q seq=%d", i, f, r.marker+"live", perSnap+1)
			}
		}(i)
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatalf("concurrent rich streams did not finish in time (possible deadlock): %v", ctx.Err())
	}

	// Disconnect every client; each server goroutine must return (no leak).
	for i := range rigs {
		_ = rigs[i].conn.Close()
	}
	for i := range rigs {
		select {
		case <-rigs[i].done:
		case <-time.After(5 * time.Second):
			t.Fatalf("client %d server goroutine did not return after disconnect", i)
		}
	}
}
