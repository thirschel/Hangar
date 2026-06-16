//go:build windows

package winhost

import (
	"testing"
	"time"
)

func TestDetectPromptCopilot(t *testing.T) {
	cases := []struct {
		name    string
		program string
		plain   string
		want    bool
	}{
		{"copilot approval", "copilot", "Do you want to run this command?\n  1. Yes\n  3. No, and tell Copilot what to do differently (Esc to stop)", true},
		{"copilot no prompt", "copilot", "thinking...\nOutput: hello", false},
		{"copilot trust is not an approval prompt", "copilot", "Do you trust the files in this folder?\n 1. Yes", false},
		{"claude still matches", "claude", "No, and tell Claude what to do differently", true},
		{"copilot string under claude program does not match", "claude", "No, and tell Copilot what to do differently", false},
		{"cmd.exe copilot path", `cmd.exe /c copilot`, "No, and tell Copilot what to do differently", true},
		{"aider", "aider", "(Y)es/(N)o/(D)on't ask again", true},
		{"gemini", "gemini", "Yes, allow once", true},
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
		{"copilot approval still counts", "copilot", "No, and tell Copilot what to do differently", true},
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
	type in struct{ enabled, attached, prompt, armed bool }
	type out struct{ tap, nextArmed bool }
	cases := []struct {
		name string
		in   in
		want out
	}{
		{"disabled never taps", in{false, false, true, true}, out{false, true}},
		{"attached never taps and keeps armed", in{true, true, true, true}, out{false, true}},
		{"rising edge taps once and disarms", in{true, false, true, true}, out{true, false}},
		{"prompt still showing but disarmed: no repeat", in{true, false, true, false}, out{false, false}},
		{"prompt cleared re-arms", in{true, false, false, false}, out{false, true}},
		{"no prompt already armed stays armed", in{true, false, false, true}, out{false, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tap, next := autoYesDecision(tc.in.enabled, tc.in.attached, tc.in.prompt, tc.in.armed)
			if tap != tc.want.tap || next != tc.want.nextArmed {
				t.Fatalf("autoYesDecision(%+v) = (tap=%v, armed=%v), want (tap=%v, armed=%v)",
					tc.in, tap, next, tc.want.tap, tc.want.nextArmed)
			}
		})
	}
}

// TestAutoYesEdgeSequence walks a full prompt lifecycle to ensure exactly one
// tap per prompt appearance across consecutive ticks.
func TestAutoYesEdgeSequence(t *testing.T) {
	armed := true
	taps := 0
	// Simulate ticks: prompt absent, then present for 3 ticks, then gone, then present again.
	prompts := []bool{false, true, true, true, false, false, true}
	for _, p := range prompts {
		tap, next := autoYesDecision(true, false, p, armed)
		if tap {
			taps++
		}
		armed = next
	}
	if taps != 2 {
		t.Fatalf("expected exactly 2 taps across two prompt appearances, got %d", taps)
	}
}

// TestAgentStatus exercises the per-session status indicator: a prompt on screen
// reads as waiting; changing output reads as busy; settled output reads as idle.
func TestAgentStatus(t *testing.T) {
	// Prompt on screen -> waiting (not busy).
	s := newConptySession("t", "copilot", "", 80, 24, false).(*conptySession)
	_, _ = s.emu.Write([]byte("Do you want to run this command?\r\n" +
		"  3. No, and tell Copilot what to do differently (Esc to stop)"))
	s.updateStatus()
	if busy, waiting := s.agentStatus(); !waiting || busy {
		t.Fatalf("prompt should be waiting (not busy), got busy=%v waiting=%v", busy, waiting)
	}

	// Changing output, no prompt -> busy.
	s2 := newConptySession("t2", "copilot", "", 80, 24, false).(*conptySession)
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
	s3 := newConptySession("t3", "copilot", "", 80, 24, false).(*conptySession)
	s3.mu.Lock()
	s3.lastInputMs = time.Now().UnixMilli()
	s3.mu.Unlock()
	_, _ = s3.emu.Write([]byte("a half-typed message"))
	s3.updateStatus()
	if busy, waiting := s3.agentStatus(); busy || waiting {
		t.Fatalf("typing echo should not be busy, got busy=%v waiting=%v", busy, waiting)
	}
}

// detectPrompt + the emulator + the attachedCnt gate (no child process needed):
// with a copilot approval prompt on screen, AutoYes fires (disarms) when not
// attached and is paused (stays armed) while a client is attached.
func TestMaybeAutoYesPausesWhileAttached(t *testing.T) {
	mk := func() *conptySession {
		s := newConptySession("t", "copilot", "", 80, 24, true).(*conptySession)
		_, _ = s.emu.Write([]byte("Do you want to run this command?\r\n" +
			"  3. No, and tell Copilot what to do differently (Esc to stop)"))
		return s
	}

	// Not attached: the rising edge should fire (sendKeys no-ops with a nil pty,
	// but the decision disarms).
	s := mk()
	s.maybeAutoYes()
	s.mu.Lock()
	armed := s.autoYesArmed
	s.mu.Unlock()
	if armed {
		t.Fatal("expected host AutoYes to fire (disarm) when not attached")
	}

	// Attached: must not fire; stays armed so the user keeps control.
	s2 := mk()
	s2.mu.Lock()
	s2.attachedCnt = 1
	s2.mu.Unlock()
	s2.maybeAutoYes()
	s2.mu.Lock()
	armed2 := s2.autoYesArmed
	s2.mu.Unlock()
	if !armed2 {
		t.Fatal("expected host AutoYes to stay armed (paused) while attached")
	}
}
