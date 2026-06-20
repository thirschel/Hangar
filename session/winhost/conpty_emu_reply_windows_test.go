//go:build windows

package winhost

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// captureWritePty is a minimal xpty.Pty whose Write records the bytes the host
// sends to the child. Every other interface method is promoted from the
// embedded (nil) xpty.Pty and is never called by pumpEmuReplies, so leaving them
// unimplemented is fine for this test.
type captureWritePty struct {
	xpty.Pty
	mu  sync.Mutex
	buf bytes.Buffer
}

func (p *captureWritePty) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Write(b)
}

func (p *captureWritePty) written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.buf.Bytes()...)
}

// TestPumpEmuRepliesUnblocksDECRQM is the regression guard for the agent-pane
// hang. The vt emulator answers a DECRQM query by writing the reply to an
// unbuffered io.Pipe, so emu.Write blocks until that reply is read. drain()
// calls emu.Write while holding subMu, so without a reader a single agent query
// wedges the whole session. pumpEmuReplies must drain the reply pipe; with it,
// emu.Write returns promptly. Remove the pump and this test times out -- exactly
// the production deadlock copilot tripped with its mode-2026 probe.
func TestPumpEmuRepliesUnblocksDECRQM(t *testing.T) {
	s := &conptySession{
		emu: vt.NewSafeEmulator(80, 24),
		pty: &captureWritePty{},
	}
	go s.pumpEmuReplies()
	t.Cleanup(func() { _ = s.emu.Close() })

	done := make(chan struct{})
	go func() {
		// mode-2026 (synchronized output) DECRQM probe -- copilot's first query.
		_, _ = s.emu.Write([]byte("\x1b[?2026$p"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emu.Write(DECRQM) blocked: pumpEmuReplies did not drain the emulator reply pipe")
	}
}

// TestPumpEmuRepliesForwardsReplies asserts the drained reply is written back to
// the child, so the host answers the agent's capability queries like a real
// terminal instead of swallowing them. A Primary Device Attributes request
// (ESC[c) elicits a deterministic CSI ? ... c response.
func TestPumpEmuRepliesForwardsReplies(t *testing.T) {
	pty := &captureWritePty{}
	s := &conptySession{
		emu: vt.NewSafeEmulator(80, 24),
		pty: pty,
	}
	go s.pumpEmuReplies()
	t.Cleanup(func() { _ = s.emu.Close() })

	if _, err := s.emu.Write([]byte("\x1b[c")); err != nil {
		t.Fatalf("emu.Write(Primary DA): %v", err)
	}

	deadline := time.After(2 * time.Second)
	for len(pty.written()) == 0 {
		select {
		case <-deadline:
			t.Fatal("pumpEmuReplies did not forward the emulator reply to the pty")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if got := pty.written(); !bytes.HasPrefix(got, []byte("\x1b[?")) {
		t.Fatalf("forwarded reply = %q, want a CSI ? device-attributes response", got)
	}
}
