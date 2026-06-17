//go:build windows

package winhost

import (
	"testing"
	"time"

	"claude-squad/session/promptpolicy"
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
