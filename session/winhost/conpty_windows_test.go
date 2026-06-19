//go:build windows

package winhost

import (
	"strings"
	"testing"
	"time"

	"hangar/session/promptpolicy"
)

func TestDetectPromptUsesPolicyClassifier(t *testing.T) {
	cases := []struct {
		name    string
		program string
		plain   string
		want    bool
	}{
		{"copilot shell prompt", "copilot", "Do you want to run this command?\n  1. Yes\n  3. No, and tell Copilot what to do differently (Esc to stop)", true},
		{"copilot no prompt", "copilot", "thinking...\nOutput: hello", false},
		{"copilot trust is a classified prompt", "copilot", "Do you trust the files in this folder?\n 1. Yes", true},
		{"claude safe continue matches", "claude", "Continue?\nNo, and tell Claude what to do differently", true},
		{"copilot string under claude program does not match", "claude", "No, and tell Copilot what to do differently", false},
		{"cmd.exe copilot path", `cmd.exe /c copilot`, "Continue?\nNo, and tell Copilot what to do differently", true},
		{"aider", "aider", "Proceed with this answer?\n(Y)es/(N)o/(D)on't ask again", true},
		{"gemini", "gemini", "Continue?\nYes, allow once", true},
		{"unknown program", "bash", "No, and tell Copilot what to do differently", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectPrompt(tc.program, tc.plain); got != tc.want {
				t.Fatalf("detectPrompt(%q, ...) = %v, want %v", tc.program, got, tc.want)
			}
		})
	}
}

func TestDetectWaiting(t *testing.T) {
	cases := []struct {
		name    string
		program string
		plain   string
		want    bool
	}{
		{"copilot selection menu", "copilot", "Question\nWhat would you like to work on?\n 1. Build a new feature\n 2. Fix a bug\n ↑/↓ to select · enter to confirm · esc to cancel", true},
		{"copilot approval still counts", "copilot", "Do you want to run this command?\nNo, and tell Copilot what to do differently", true},
		{"yes/no prompt", "aider", "Apply changes? (Y)es/(N)o", true},
		{"press enter to continue", "copilot", "Press enter to continue", true},
		{"copilot banner is not waiting", "copilot", "Copilot v1.0.63 uses AI.\nCheck for mistakes.\nTip: /resume", false},
		{"plain output is not waiting", "copilot", "thinking...\nwriting code", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectWaiting(tc.program, tc.plain); got != tc.want {
				t.Fatalf("detectWaiting(%q, ...) = %v, want %v", tc.program, got, tc.want)
			}
		})
	}
}

func TestAutoYesDecision(t *testing.T) {
	allowMatch := mustClassify(t, "copilot", "Press enter to continue")
	shellMatch := mustClassify(t, "copilot", "Do you want to run this command?\nNo, and tell Copilot what to do differently")
	trustMatch := mustClassify(t, "copilot", "Do you trust the files in this folder?")
	pending := &pendingTrustApproval{reason: "test", expiresAt: time.Now().Add(time.Minute)}
	cases := []struct {
		name        string
		enabled     bool
		attached    bool
		match       any
		pending     *pendingTrustApproval
		wantApprove bool
		wantConsume bool
		wantSource  string
	}{
		{"disabled denies safe prompt", false, false, allowMatch, nil, false, false, ""},
		{"attached pauses safe prompt", true, true, allowMatch, nil, false, false, ""},
		{"policy allows safe prompt", true, false, allowMatch, nil, true, false, "policy"},
		{"policy blocks shell prompt", true, false, shellMatch, nil, false, false, ""},
		{"pending trust allows trust while attached", false, true, trustMatch, pending, true, true, "force-once:test"},
		{"pending trust does not allow shell", false, true, shellMatch, pending, false, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			match := tc.match.(promptpolicy.Match)
			approve, consume, source := autoYesDecision(tc.enabled, tc.attached, match, tc.pending, time.Now())
			if approve != tc.wantApprove || consume != tc.wantConsume || source != tc.wantSource {
				t.Fatalf("autoYesDecision = (approve=%v consume=%v source=%q), want (%v %v %q)",
					approve, consume, source, tc.wantApprove, tc.wantConsume, tc.wantSource)
			}
		})
	}
}

func mustClassify(t *testing.T, program, text string) promptpolicy.Match {
	t.Helper()
	match, ok := promptpolicy.Classify(program, text)
	if !ok {
		t.Fatalf("expected classification for %q", text)
	}
	return match
}

// TestAgentStatus exercises the per-session status indicator: a prompt on screen
// reads as waiting; changing output reads as busy; settled output reads as idle.
func TestAgentStatus(t *testing.T) {
	// Prompt on screen -> waiting (not busy).
	s := newConptySession("t", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	_, _ = s.emu.Write([]byte("Do you want to run this command?\r\n" +
		"  3. No, and tell Copilot what to do differently (Esc to stop)"))
	s.updateStatus()
	if busy, waiting := s.agentStatus(); !waiting || busy {
		t.Fatalf("prompt should be waiting (not busy), got busy=%v waiting=%v", busy, waiting)
	}

	// Changing output, no prompt -> busy.
	s2 := newConptySession("t2", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	_, _ = s2.emu.Write([]byte("thinking...\r\nwriting code\r\n"))
	s2.updateStatus()
	if busy, waiting := s2.agentStatus(); !busy || waiting {
		t.Fatalf("changing output should be busy, got busy=%v waiting=%v", busy, waiting)
	}

	// Settled (no further change) past the busy window -> idle.
	time.Sleep(1600 * time.Millisecond)
	s2.updateStatus()
	if busy, waiting := s2.agentStatus(); busy || waiting {
		t.Fatalf("settled output should be idle, got busy=%v waiting=%v", busy, waiting)
	}

	// Content changing right after user input is the keystrokes echoing to the
	// screen, not the agent working -> not busy.
	s3 := newConptySession("t3", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	s3.mu.Lock()
	s3.lastInputMs = time.Now().UnixMilli()
	s3.mu.Unlock()
	_, _ = s3.emu.Write([]byte("a half-typed message"))
	s3.updateStatus()
	if busy, waiting := s3.agentStatus(); busy || waiting {
		t.Fatalf("typing echo should not be busy, got busy=%v waiting=%v", busy, waiting)
	}
}

// The emulator + attachedCnt gate (no child process needed): with a benign
// prompt on screen, AutoYes fires once when not attached and pauses while a
// client is attached.
func TestMaybeAutoYesPausesWhileAttached(t *testing.T) {
	mk := func() *conptySession {
		s := newConptySession("t", "copilot", "", "cmd", 80, 24, true).(*conptySession)
		_, _ = s.emu.Write([]byte("Press enter to continue"))
		return s
	}

	// Not attached: the prompt should be policy-approved once.
	s := mk()
	s.maybeAutoYes()
	s.mu.Lock()
	fp := s.lastPromptFP
	s.mu.Unlock()
	if fp == "" {
		t.Fatal("expected host AutoYes to record the approved prompt fingerprint")
	}

	// Attached: must not fire; fingerprint stays empty so the user keeps control.
	s2 := mk()
	s2.mu.Lock()
	s2.attachedCnt = 1
	s2.mu.Unlock()
	s2.maybeAutoYes()
	s2.mu.Lock()
	fp2 := s2.lastPromptFP
	s2.mu.Unlock()
	if fp2 != "" {
		t.Fatal("expected host AutoYes to stay paused while attached")
	}
}

func TestMaybeAutoYesForceApprovalIsScopedToTrustFolder(t *testing.T) {
	s := newConptySession("t", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	s.armTrustApproval("unit", time.Now().Add(time.Minute))
	_, _ = s.emu.Write([]byte("Do you want to run this command?\r\nNo, and tell Copilot what to do differently"))
	s.maybeAutoYes()
	s.mu.Lock()
	pendingAfterShell := s.pendingTrust != nil
	fpAfterShell := s.lastPromptFP
	s.mu.Unlock()
	if !pendingAfterShell {
		t.Fatal("shell prompt must not consume one-shot trust approval")
	}
	if fpAfterShell != "" {
		t.Fatal("shell prompt must not be force-approved")
	}

	s2 := newConptySession("t2", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	s2.mu.Lock()
	s2.attachedCnt = 1
	s2.mu.Unlock()
	s2.armTrustApproval("unit", time.Now().Add(time.Minute))
	_, _ = s2.emu.Write([]byte("Do you trust the files in this folder?"))
	s2.maybeAutoYes()
	s2.mu.Lock()
	pendingAfterTrust := s2.pendingTrust != nil
	fpAfterTrust := s2.lastPromptFP
	s2.mu.Unlock()
	if pendingAfterTrust {
		t.Fatal("trust prompt must consume one-shot approval")
	}
	if fpAfterTrust == "" {
		t.Fatal("trust prompt should be approved despite attached user only for the one-shot")
	}
}

// TestAutoYesTickNeededSkipsWhenWriteGenUnchanged exercises the idle-CPU gate:
// the autoYesLoop renders/hashes/classifies only when the emulator write
// generation advanced (drain wrote output) or a trust approval is pending; an
// idle session whose generation is unchanged is skipped and costs nothing.
func TestAutoYesTickNeededSkipsWhenWriteGenUnchanged(t *testing.T) {
	s := newConptySession("gate", "copilot", "", "cmd", 80, 24, false).(*conptySession)

	// The first tick (not yet primed) always runs so the status baseline is
	// established, even before any output (write generation still 0).
	if !s.autoYesTickNeeded(s.writeGen.Load(), 0, false) {
		t.Fatal("first tick (not primed) must always run")
	}

	// drain() bumps writeGen after each emulator write; a new generation means the
	// screen may have changed, so a primed tick must run.
	_, _ = s.emu.Write([]byte("thinking...\r\n"))
	s.writeGen.Add(1)
	gen := s.writeGen.Load()
	if gen == 0 {
		t.Fatal("writeGen.Add should advance the generation")
	}
	if !s.autoYesTickNeeded(gen, 0, true) {
		t.Fatal("a changed write generation must run the tick")
	}

	// No new output: the generation matches the previous tick -> skip. This is the
	// win that makes idle and detached sessions effectively free.
	if s.autoYesTickNeeded(gen, gen, true) {
		t.Fatal("an unchanged write generation must skip the tick")
	}

	// A pending trust approval expires on a timer, not on screen output, so the
	// loop must keep running even while the generation is unchanged.
	s.armTrustApproval("unit", time.Now().Add(time.Minute))
	if !s.autoYesTickNeeded(gen, gen, true) {
		t.Fatal("must not skip while a trust approval is pending (its expiry is time-based)")
	}
	s.clearTrustApproval()
	if s.autoYesTickNeeded(gen, gen, true) {
		t.Fatal("must skip again once the trust approval is cleared")
	}
}

// TestSetAutoYesWakesGatedLoopOnStaticScreen guards the interaction between the
// change-gate and the runtime SetAutoYes RPC: enabling AutoYes while the agent is
// blocked on an already-displayed prompt produces no new emulator output, so the
// generation does not advance on its own. setAutoYes(true) must bump writeGen so
// the next gated tick still runs and policy-evaluates the on-screen prompt;
// otherwise the prompt would never be auto-approved (the agent would hang).
func TestSetAutoYesWakesGatedLoopOnStaticScreen(t *testing.T) {
	s := newConptySession("wake", "copilot", "", "cmd", 80, 24, false).(*conptySession)

	// A prompt arrives and is rendered; the gated loop processes it and catches up
	// to this generation. AutoYes is still off, so nothing was approved.
	_, _ = s.emu.Write([]byte("Do you want to run this command?\r\n" +
		"  1. Yes\r\n  3. No, and tell Copilot what to do differently (Esc to stop)"))
	s.writeGen.Add(1)
	gen := s.writeGen.Load()

	// Screen is now static (agent blocked waiting for input): a primed tick whose
	// generation matches the last one would skip.
	if s.autoYesTickNeeded(gen, gen, true) {
		t.Fatal("precondition: a static screen with AutoYes off should skip")
	}

	// Enabling AutoYes at runtime must wake the loop even though no new output
	// occurred, so the already-displayed prompt gets evaluated.
	s.setAutoYes(true)
	if s.writeGen.Load() == gen {
		t.Fatal("setAutoYes(true) must advance writeGen to wake the gated loop")
	}
	if !s.autoYesTickNeeded(s.writeGen.Load(), gen, true) {
		t.Fatal("after setAutoYes(true) the next tick must run on a static prompt")
	}
}

// TestUpdateStatusFromMatchesUpdateStatus proves the render-once refactor is
// behavior-preserving: updateStatusFrom over a prerendered screen reaches the
// same status state as updateStatus rendering the screen itself.
func TestUpdateStatusFromMatchesUpdateStatus(t *testing.T) {
	content := []byte("Do you want to run this command?\r\n" +
		"  3. No, and tell Copilot what to do differently (Esc to stop)")

	a := newConptySession("a", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	_, _ = a.emu.Write(content)
	a.updateStatus()

	b := newConptySession("b", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	_, _ = b.emu.Write(content)
	b.updateStatusFrom(plainScreen(b.emu))

	a.mu.Lock()
	aHash, aPrompt := a.statusHash, a.statusPrompt
	a.mu.Unlock()
	b.mu.Lock()
	bHash, bPrompt := b.statusHash, b.statusPrompt
	b.mu.Unlock()
	if aHash != bHash || aPrompt != bPrompt {
		t.Fatalf("updateStatusFrom diverged: hash %q vs %q, prompt %v vs %v", bHash, aHash, bPrompt, aPrompt)
	}
	if ba, wa := a.agentStatus(); !wa || ba {
		t.Fatalf("updateStatus: prompt should read waiting, got busy=%v waiting=%v", ba, wa)
	}
	if bb, wb := b.agentStatus(); !wb || bb {
		t.Fatalf("updateStatusFrom: prompt should read waiting, got busy=%v waiting=%v", bb, wb)
	}
}

// TestUpdateStatusFromUnchangedContentIsNoOp proves skipping a tick is safe:
// re-processing the same plain screen does not advance lastChangeMs (which would
// falsely re-mark the agent busy), so a skipped unchanged tick loses nothing.
func TestUpdateStatusFromUnchangedContentIsNoOp(t *testing.T) {
	s := newConptySession("noop", "copilot", "", "cmd", 80, 24, false).(*conptySession)
	_, _ = s.emu.Write([]byte("thinking...\r\nwriting code\r\n"))
	plain := plainScreen(s.emu)

	s.updateStatusFrom(plain)
	s.mu.Lock()
	first := s.lastChangeMs
	s.mu.Unlock()
	if first == 0 {
		t.Fatal("changing content should set lastChangeMs")
	}

	time.Sleep(10 * time.Millisecond)
	s.updateStatusFrom(plain) // same content -> must be a no-op for lastChangeMs
	s.mu.Lock()
	second := s.lastChangeMs
	s.mu.Unlock()
	if second != first {
		t.Fatalf("unchanged content advanced lastChangeMs (%d -> %d); skipping would not be equivalent", first, second)
	}
}

// TestMaybeAutoYesFromMatchesMaybeAutoYes proves the AutoYes classifier reaches
// the same decision whether it renders the screen itself (maybeAutoYes) or runs
// over the prerendered screen the loop shares (maybeAutoYesFrom).
func TestMaybeAutoYesFromMatchesMaybeAutoYes(t *testing.T) {
	mk := func(name string) *conptySession {
		s := newConptySession(name, "copilot", "", "cmd", 80, 24, true).(*conptySession)
		_, _ = s.emu.Write([]byte("Press enter to continue"))
		return s
	}

	a := mk("a")
	a.maybeAutoYes()
	a.mu.Lock()
	fpA := a.lastPromptFP
	a.mu.Unlock()

	b := mk("b")
	b.maybeAutoYesFrom(plainScreen(b.emu))
	b.mu.Lock()
	fpB := b.lastPromptFP
	b.mu.Unlock()

	if fpA == "" || fpB == "" {
		t.Fatalf("expected both paths to approve the safe prompt, got fpA=%q fpB=%q", fpA, fpB)
	}
	if fpA != fpB {
		t.Fatalf("maybeAutoYesFrom recorded a different fingerprint: %q vs %q", fpB, fpA)
	}
}

func TestScrollbackANSI(t *testing.T) {
	s := newConptySession("hist", "copilot", "", "cmd", 40, 3, false).(*conptySession)
	_, _ = s.emu.Write([]byte("\x1b[31mRED-00\x1b[0m\r\n" +
		"PLAIN-01\r\n" +
		"\x1b[32mGREEN-02\x1b[0m\r\n" +
		"PLAIN-03\r\n" +
		"PLAIN-04\r\n" +
		"PLAIN-05\r\n"))

	if got := s.emu.ScrollbackLen(); got == 0 {
		t.Fatal("expected scrollback after writing more lines than emulator height")
	}
	ansi := scrollbackANSI(s.emu, s.emu.Width())
	for _, want := range []string{"RED-00", "PLAIN-01", "GREEN-02"} {
		if !strings.Contains(ansi, want) {
			t.Fatalf("scrollback ANSI missing %q: %q", want, ansi)
		}
	}
	if !strings.Contains(ansi, "\x1b[31m") && !strings.Contains(ansi, "\x1b[38;") {
		t.Fatalf("scrollback ANSI missing red SGR sequence: %q", ansi)
	}
	last := -1
	for _, want := range []string{"RED-00", "PLAIN-01", "GREEN-02"} {
		idx := strings.Index(ansi, want)
		if idx <= last {
			t.Fatalf("scrollback order is not top-to-bottom for %q in %q", want, ansi)
		}
		last = idx
	}
	if !strings.Contains(ansi, "\r\n") {
		t.Fatalf("scrollback rows should be CRLF-terminated: %q", ansi)
	}
}

func TestCaptureHistoryAltScreen(t *testing.T) {
	s := newConptySession("alt", "copilot", "", "cmd", 40, 3, false).(*conptySession)
	_, _ = s.emu.Write([]byte("\x1b[?1049hALT-SCREEN"))
	if !s.emu.IsAltScreen() {
		t.Fatal("emulator did not enter alternate screen")
	}
	ansi, altScreen, lines := s.captureHistory(true, 0, 0)
	if !altScreen {
		t.Fatal("captureHistory did not report alternate screen")
	}
	if lines != 0 {
		t.Fatalf("expected no main scrollback while only alternate-screen output was written, got %d lines", lines)
	}
	if !strings.Contains(ansi, "ALT-SCREEN") {
		t.Fatalf("includeScreen capture did not include alternate visible screen: %q", ansi)
	}

	_, _ = s.emu.Write([]byte("\x1b[?1049l"))
	if s.emu.IsAltScreen() {
		t.Fatal("emulator did not leave alternate screen")
	}
	_, altScreen, _ = s.captureHistory(false, 0, 0)
	if altScreen {
		t.Fatal("captureHistory still reported alternate screen after leaving it")
	}
}

func TestTrackAndReplayModes(t *testing.T) {
	s := &conptySession{decModes: make(map[int]bool)}
	s.trackModesLocked([]byte("\x1b[?1049h\x1b[?1002;1006h\x1b[?2004h\x1b[?9001hHELLO"))

	for _, m := range []int{1049, 1002, 1006, 2004, 9001} {
		if !s.decModes[m] {
			t.Fatalf("mode %d not tracked: %v", m, s.decModes)
		}
	}

	replay := string(s.replayModesLocked())
	// Alt-screen must come first so the snapshot's clear/home/render lands in the
	// alternate buffer rather than accumulating in the normal buffer.
	if !strings.HasPrefix(replay, "\x1b[?1049h") {
		t.Fatalf("replay should start with alt-screen enter, got %q", replay)
	}
	for _, want := range []string{"\x1b[?1002h", "\x1b[?1006h", "\x1b[?2004h"} {
		if !strings.Contains(replay, want) {
			t.Fatalf("replay missing %q, got %q", want, replay)
		}
	}
	// 9001 (win32-input) is deny-listed and must not be replayed to xterm.
	if strings.Contains(replay, "\x1b[?9001h") {
		t.Fatalf("replay must not contain deny-listed 9001, got %q", replay)
	}

	// A reset clears the mode.
	s.trackModesLocked([]byte("\x1b[?1049l"))
	if s.decModes[1049] {
		t.Fatal("mode 1049 should be cleared after reset")
	}
}

func TestTrackModesSplitAcrossChunks(t *testing.T) {
	s := &conptySession{decModes: make(map[int]bool)}
	// The mode sequence ESC[?1049h is split across two reads.
	s.trackModesLocked([]byte("output\x1b[?10"))
	s.trackModesLocked([]byte("49h more output"))
	if !s.decModes[1049] {
		t.Fatalf("mode split across chunks not tracked: %v", s.decModes)
	}
}

func TestReplayModesEmptyWhenNoModes(t *testing.T) {
	s := &conptySession{decModes: make(map[int]bool)}
	if got := s.replayModesLocked(); got != nil {
		t.Fatalf("expected nil replay when no modes are active, got %q", string(got))
	}
}
