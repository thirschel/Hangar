//go:build windows

package winhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"hangar/session/agentcmd"
	"hangar/session/promptpolicy"
	"hangar/session/winhost/proto"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// rawRingMax bounds the raw-output ring kept per session. It is a supplementary
// repaint source; the authoritative attach repaint is the emulator snapshot.
const rawRingMax = 256 * 1024

// subscriber receives live raw output from a session while a client is attached.
type subscriber struct {
	ch chan []byte
}

// conptySession runs an interactive program in a Windows ConPTY and continuously
// renders its output through a VT emulator so the screen can be captured at any
// time (the tmux-capture-pane equivalent).
type conptySession struct {
	name    string
	program string
	workDir string
	shell   string // "cmd", "powershell", "pwsh"

	pty xpty.Pty
	cmd *exec.Cmd
	emu *vt.SafeEmulator

	writeMu sync.Mutex // serializes writes to the child's input

	// subMu guards subs AND makes {emu.Write + fan-out} atomic w.r.t. subscribe's
	// snapshot, so an attaching client gets a clean snapshot then a gap-free,
	// non-duplicated live stream.
	subMu sync.Mutex
	subs  map[*subscriber]struct{}

	mu           sync.Mutex
	autoYes      bool
	pendingTrust *pendingTrustApproval
	lastPromptFP string
	attachedCnt  int // >0 while a client is interactively attached (AutoYes pauses)
	cols         int
	rows         int
	detCols      int // detached/preview size to restore when a client detaches
	detRows      int
	rawRing      []byte
	aliveFlag    bool
	exitCode     int
	prevHash     string
	statusHash   string // separate from prevHash: tracks content for the status indicator
	lastChangeMs int64  // last time the screen content changed due to agent output (UnixMilli)
	lastInputMs  int64  // last time the user sent keystrokes (UnixMilli)
	statusPrompt bool   // cached detectPrompt result (waiting for input)

	drainDone   chan struct{}
	autoYesStop chan struct{}
	closeOnce   sync.Once
}

type pendingTrustApproval struct {
	reason    string
	expiresAt time.Time
}

func newConptySession(name, program, workDir, shell string, cols, rows int, autoYes bool) managedSession {
	return &conptySession{
		name:        name,
		program:     program,
		workDir:     workDir,
		shell:       shell,
		cols:        cols,
		rows:        rows,
		detCols:     cols,
		detRows:     rows,
		autoYes:     autoYes,
		emu:         vt.NewSafeEmulator(cols, rows),
		subs:        make(map[*subscriber]struct{}),
		autoYesStop: make(chan struct{}),
	}
}

func (s *conptySession) start() error {
	// Build the launch spec ONCE: the program string is tokenized with
	// platform-correct (CommandLineToArgvW) semantics and, in the default path,
	// launched directly via argv with NO shell interpreter. This removes the
	// cmd.exe/powershell middleman that allowed `&`/`;`/`|` in an agent program
	// or a poisoned resume id to inject commands (F-01/F-04). Any resume id is
	// already validated at the trust boundary and survives as a single argv
	// element.
	spec, err := agentcmd.BuildLaunch(s.program, "", "--resume=", agentcmd.ParseShellKind(s.shell))
	if err != nil {
		return err
	}
	pty, err := xpty.NewPty(s.cols, s.rows)
	if err != nil {
		return fmt.Errorf("create conpty: %w", err)
	}

	var cmd *exec.Cmd
	switch spec.Shell {
	case agentcmd.ShellPowerShell:
		// Explicit opt-in shell mode (e.g. PowerShell-function agents like `cpa`).
		// The script is the trusted program text; any resume id is charset-
		// restricted and/or env-bound so it cannot inject.
		cmd = exec.Command("powershell.exe", "-NoLogo", "-Command", spec.Script)
		cmd.Env = append(os.Environ(), spec.Env...)
	case agentcmd.ShellPwsh:
		cmd = exec.Command("pwsh.exe", "-NoLogo", "-Command", spec.Script)
		cmd.Env = append(os.Environ(), spec.Env...)
	default: // ShellNone: direct argv exec, no shell.
		if spec.Path == "" {
			_ = pty.Close()
			return fmt.Errorf("empty program")
		}
		cmd = exec.Command(spec.Path, spec.Args...)
	}
	if s.workDir != "" {
		cmd.Dir = s.workDir
	}
	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return fmt.Errorf("start program: %w", err)
	}
	s.pty = pty
	s.cmd = cmd
	s.mu.Lock()
	s.aliveFlag = true
	s.mu.Unlock()
	s.drainDone = make(chan struct{})
	go s.drain()
	go s.wait()
	go s.autoYesLoop()
	return nil
}

// drain continuously reads ConPTY output and feeds the emulator + raw ring +
// any attached subscribers. It must never block on a slow subscriber (the child
// would stall on a full output pipe), so fan-out is non-blocking.
func (s *conptySession) drain() {
	defer close(s.drainDone)
	defer recoverGoroutine("conpty.drain")
	buf := make([]byte, 4096)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			data := buf[:n]
			s.subMu.Lock()
			_, _ = s.emu.Write(data) // SafeEmulator is internally locked
			s.mu.Lock()
			s.rawRing = appendRing(s.rawRing, data, rawRingMax)
			s.mu.Unlock()
			if len(s.subs) > 0 {
				cp := append([]byte(nil), data...)
				for sub := range s.subs {
					select {
					case sub.ch <- cp:
					default:
						// Subscriber too slow; drop this chunk rather than stall
						// the child. The emulator stays authoritative for capture.
					}
				}
			}
			s.subMu.Unlock()
		}
		if err != nil {
			s.subMu.Lock()
			for sub := range s.subs {
				close(sub.ch)
				delete(s.subs, sub)
			}
			s.subMu.Unlock()
			return
		}
	}
}

// subscribe resizes to the attaching client's console size, then atomically
// snapshots the rendered screen and registers a subscriber for the live stream.
func (s *conptySession) subscribe(cols, rows int) ([]byte, *subscriber) {
	if cols > 0 && rows > 0 {
		_ = s.applyResize(cols, rows)
	}
	s.mu.Lock()
	s.attachedCnt++
	s.mu.Unlock()
	s.subMu.Lock()
	defer s.subMu.Unlock()
	// Clear+home, then the rendered screen, so the client repaints cleanly.
	snap := append([]byte("\x1b[2J\x1b[H"), []byte(s.emu.Render())...)
	sub := &subscriber{ch: make(chan []byte, 1024)}
	s.subs[sub] = struct{}{}
	return snap, sub
}

// unsubscribe removes a subscriber and resizes back to the detached/preview size.
func (s *conptySession) unsubscribe(sub *subscriber) {
	s.subMu.Lock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		close(sub.ch)
	}
	s.subMu.Unlock()
	s.mu.Lock()
	if s.attachedCnt > 0 {
		s.attachedCnt--
	}
	dc, dr := s.detCols, s.detRows
	s.mu.Unlock()
	_ = s.applyResize(dc, dr)
}

// wait blocks until the child exits, records the exit, and closes the PTY so the
// drain goroutine returns.
func (s *conptySession) wait() {
	defer recoverGoroutine("conpty.wait")
	_ = xpty.WaitProcess(context.Background(), s.cmd)
	s.mu.Lock()
	s.aliveFlag = false
	if s.cmd.ProcessState != nil {
		s.exitCode = s.cmd.ProcessState.ExitCode()
	}
	s.mu.Unlock()
	_ = s.close()
}

func (s *conptySession) sendKeys(b []byte) error {
	s.mu.Lock()
	s.lastInputMs = time.Now().UnixMilli()
	s.mu.Unlock()
	if s.pty == nil {
		return fmt.Errorf("session not started")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.pty.Write(b)
	return err
}

func (s *conptySession) resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	s.mu.Lock()
	s.detCols, s.detRows = cols, rows
	s.mu.Unlock()
	return s.applyResize(cols, rows)
}

// applyResize resizes the ConPTY and emulator to the given size without changing
// the remembered detached/preview size.
func (s *conptySession) applyResize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	s.emu.Resize(cols, rows)
	if s.pty != nil {
		return s.pty.Resize(cols, rows)
	}
	return nil
}

func (s *conptySession) capture(full, withANSI bool) string {
	if !full {
		if withANSI {
			return s.emu.Render()
		}
		return plainScreen(s.emu)
	}
	// full = scrollback history + visible screen (plain). ANSI-encoded scrollback
	// is a later refinement; the history-scroll/diff consumer needs plain text.
	sb := scrollbackPlain(s.emu)
	scr := plainScreen(s.emu)
	if sb == "" {
		return scr
	}
	return sb + "\n" + scr
}

func (s *conptySession) hasUpdated() (bool, bool) {
	plain := plainScreen(s.emu)
	sum := sha256.Sum256([]byte(plain))
	h := hex.EncodeToString(sum[:])
	s.mu.Lock()
	changed := h != s.prevHash
	s.prevHash = h
	s.mu.Unlock()
	return changed, detectPrompt(s.program, plain)
}

// Status indicator timing. A content change within statusInputEchoMs of the
// user's last keystroke is treated as that keystroke echoing to the screen (not
// agent activity). The agent reads "busy" while content has changed within
// statusBusyWindowMs.
const (
	statusInputEchoMs  = 600
	statusBusyWindowMs = 1500
)

// updateStatus samples the agent's screen for the status indicator: it records
// when the visible content last changed *due to agent output* and whether a
// prompt is currently shown. Side-effect-free for readers; called from the
// autoYesLoop tick so it runs regardless of whether AutoYes is enabled.
func (s *conptySession) updateStatus() {
	plain := plainScreen(s.emu)
	sum := sha256.Sum256([]byte(plain))
	h := hex.EncodeToString(sum[:])
	prompt := detectWaiting(s.program, plain)
	now := time.Now().UnixMilli()
	s.mu.Lock()
	if h != s.statusHash {
		s.statusHash = h
		// Ignore changes that immediately follow user input: those are the user's
		// own keystrokes echoing to the screen, not the agent doing work.
		if now-s.lastInputMs > statusInputEchoMs {
			s.lastChangeMs = now
		}
	}
	s.statusPrompt = prompt
	s.mu.Unlock()
}

// agentStatus reports what the agent is doing, for the UI indicator:
//   - waiting: the screen shows a prompt awaiting the user's input.
//   - busy: the content changed very recently (the agent is producing output) and
//     it is not waiting.
//
// Both false means idle/ready.
func (s *conptySession) agentStatus() (busy, waiting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiting = s.statusPrompt
	busy = !waiting && s.lastChangeMs != 0 && (time.Now().UnixMilli()-s.lastChangeMs) < statusBusyWindowMs
	return busy, waiting
}

func (s *conptySession) setAutoYes(enabled bool) {
	s.mu.Lock()
	s.autoYes = enabled
	if enabled {
		// Re-arm so a prompt that is already on screen gets policy-evaluated.
		s.lastPromptFP = ""
	}
	s.mu.Unlock()
}

func (s *conptySession) armTrustApproval(reason string, expiresAt time.Time) {
	s.mu.Lock()
	s.pendingTrust = &pendingTrustApproval{reason: reason, expiresAt: expiresAt}
	s.lastPromptFP = ""
	s.mu.Unlock()
}

func (s *conptySession) clearTrustApproval() {
	s.mu.Lock()
	s.pendingTrust = nil
	s.mu.Unlock()
}

// autoYesLoop drives host-side AutoYes: it periodically classifies the visible
// prompt and approves only policy-allowlisted categories. Running it in the host
// (not the TUI) means unattended agents keep progressing even when the TUI is
// closed, while pausing whenever a user is interactively attached.
func (s *conptySession) autoYesLoop() {
	defer recoverGoroutine("conpty.autoYesLoop")
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.autoYesStop:
			return
		case <-ticker.C:
			s.updateStatus()
			s.maybeAutoYes()
		}
	}
}

func (s *conptySession) maybeAutoYes() {
	s.mu.Lock()
	enabled := s.autoYes
	attached := s.attachedCnt > 0
	lastFP := s.lastPromptFP
	pending := s.pendingTrust
	s.mu.Unlock()

	match, ok := promptpolicy.Classify(s.program, plainScreen(s.emu))
	now := time.Now()
	if !ok {
		s.mu.Lock()
		s.lastPromptFP = ""
		if s.pendingTrust != nil && now.After(s.pendingTrust.expiresAt) {
			s.pendingTrust = nil
		}
		s.mu.Unlock()
		return
	}
	if match.Fingerprint == lastFP {
		return
	}

	approve, consumePending, source := autoYesDecision(enabled, attached, match, pending, now)
	if consumePending {
		s.mu.Lock()
		if s.pendingTrust == pending {
			s.pendingTrust = nil
		}
		s.mu.Unlock()
	}
	if !approve {
		return
	}
	s.mu.Lock()
	s.lastPromptFP = match.Fingerprint
	s.mu.Unlock()
	if err := s.sendKeys(match.ApproveKeys); err != nil {
		return
	}
	promptpolicy.AuditAutoApproval(s.name, s.program, match, source)
}

func autoYesDecision(enabled, attached bool, match promptpolicy.Match, pending *pendingTrustApproval, now time.Time) (approve, consumePending bool, source string) {
	if pending != nil && now.After(pending.expiresAt) {
		return false, true, ""
	}
	if pending != nil && match.Category == promptpolicy.CategoryTrustFolder {
		return true, true, "force-once:" + pending.reason
	}
	if attached || !enabled {
		return false, false, ""
	}
	if promptpolicy.AllowsAutoApprove(match) {
		return true, false, "policy"
	}
	return false, false, ""
}

func (s *conptySession) info() proto.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.SessionInfo{Name: s.name, Alive: s.aliveFlag, ExitCode: s.exitCode, Program: s.program}
}

func (s *conptySession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aliveFlag
}

// close closes the ConPTY (terminating the child) once. Closing the PTY makes
// the drain goroutine's Read return, ending it.
func (s *conptySession) close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.autoYesStop) // stop the AutoYes ticker
		if s.pty != nil {
			err = s.pty.Close()
		}
	})
	return err
}

// appendRing appends data to ring, keeping at most max bytes (most recent).
func appendRing(ring, data []byte, max int) []byte {
	ring = append(ring, data...)
	if len(ring) > max {
		ring = ring[len(ring)-max:]
	}
	return ring
}

// plainScreen renders the visible screen as plain text (trailing spaces trimmed
// per line). Used for prompt matching and non-ANSI capture.
func plainScreen(se *vt.SafeEmulator) string {
	w, h := se.Width(), se.Height()
	lines := make([]string, 0, h)
	for y := 0; y < h; y++ {
		lines = append(lines, strings.TrimRight(rowText(se, y, false, w), " "))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func scrollbackPlain(se *vt.SafeEmulator) string {
	n := se.ScrollbackLen()
	if n == 0 {
		return ""
	}
	w := se.Width()
	lines := make([]string, 0, n)
	for y := 0; y < n; y++ {
		lines = append(lines, strings.TrimRight(rowText(se, y, true, w), " "))
	}
	return strings.Join(lines, "\n")
}

func rowText(se *vt.SafeEmulator, y int, scrollback bool, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		var c *uv.Cell
		if scrollback {
			c = se.ScrollbackCellAt(x, y)
		} else {
			c = se.CellAt(x, y)
		}
		if c == nil || c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	return b.String()
}

// detectPrompt reports prompts known to the typed prompt policy. It is used only
// for status/metadata; AutoYes approval is gated by category in maybeAutoYes.
func detectPrompt(program, plain string) bool {
	return promptpolicy.IsPrompt(program, plain)
}

// detectWaiting is a broader, status-only signal than detectPrompt: it reports
// whether the agent appears to be blocking for the user's input. It includes the
// AutoYes approval prompts PLUS common interactive selection/confirmation footers
// (e.g. copilot's "↑/↓ to select · enter to confirm · esc to cancel"). It must NOT
// drive AutoYes — tapping Enter on a selection menu would pick the default option.
func detectWaiting(program, plain string) bool {
	if detectPrompt(program, plain) {
		return true
	}
	low := strings.ToLower(plain)
	hints := []string{
		"esc to cancel",
		"enter to confirm",
		"to select", // "↑/↓ to select" selection menus
		"(y)es/(n)o",
		"[y/n]",
		"press enter",
	}
	for _, h := range hints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}
