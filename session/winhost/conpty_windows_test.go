//go:build windows

package winhost

import "testing"

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
