//go:build windows

package winhost

import (
	"strings"
	"testing"
)

// These tests exercise the additive scrollback width plumbing without a real
// ConPTY child: like the existing TestScrollbackANSI / TestCaptureHistoryAltScreen
// tests, they drive a real vt.SafeEmulator directly via newConptySession(...).emu,
// so they run headless. They assert on CONTENT MARKERS only -- never on trailing
// padding, which renderScrollbackANSIRow trims at end of line.

// TestScrollbackANSIClipsAndRevealsToWidth proves scrollbackANSI renders stored
// rows at the caller's width: CLIPPING content past a narrow width and REVEALING
// it at a wider one, while never changing the stored-row count (vt does not
// reflow scrollback). A width of 0 falls back to the live emulator width.
func TestScrollbackANSIClipsAndRevealsToWidth(t *testing.T) {
	s := newConptySession("hist", "copilot", "", "cmd", 40, 3, false, nil).(*conptySession)
	// Each line has a left marker at column 0 and a right-edge marker pushed to
	// column 20 (well past the narrow width 8). Writing more than the height (3)
	// lines forces rows into scrollback.
	line := "LEFT" + strings.Repeat(" ", 16) + "RIGHTEDGE\r\n"
	for i := 0; i < 6; i++ {
		_, _ = s.emu.Write([]byte(line))
	}

	n := s.emu.ScrollbackLen()
	if n == 0 {
		t.Fatal("expected scrollback after writing more lines than the emulator height")
	}

	// Clip to width 8: the right-edge marker (column 20) is dropped, and clipping
	// never adds rows -- one stored row maps to one CRLF-terminated output row.
	clipped := scrollbackANSI(s.emu, 8)
	if strings.Contains(clipped, "RIGHTEDGE") {
		t.Fatalf("width-8 clip should drop the right-edge marker: %q", clipped)
	}
	if got := strings.Count(clipped, "\r\n"); got != n {
		t.Fatalf("clipped row count = %d, want %d (one per stored row): %q", got, n, clipped)
	}

	// Reveal at the full width: both markers reappear.
	revealed := scrollbackANSI(s.emu, 40)
	if !strings.Contains(revealed, "LEFT") {
		t.Fatalf("width-40 reveal missing left marker: %q", revealed)
	}
	if !strings.Contains(revealed, "RIGHTEDGE") {
		t.Fatalf("width-40 reveal missing right-edge marker: %q", revealed)
	}

	// Width 0 falls back to the live emulator width.
	if got, want := scrollbackANSI(s.emu, 0), scrollbackANSI(s.emu, s.emu.Width()); got != want {
		t.Fatalf("width-0 fallback mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// TestScrollbackNoReflowOnResize proves vt clips/reveals but NEVER reflows stored
// scrollback: a line wrapped at the original width keeps its stored row boundary
// after a wider Resize (the wrapped halves are not merged back together).
func TestScrollbackNoReflowOnResize(t *testing.T) {
	s := newConptySession("nr", "copilot", "", "cmd", 10, 2, false, nil).(*conptySession)
	// 16 chars wrap at width 10 into stored rows "ABCDEFGHIJ" then "KLMNOP". Two
	// more short lines evict BOTH wrapped rows fully into scrollback.
	_, _ = s.emu.Write([]byte("ABCDEFGHIJKLMNOP\r\n"))
	_, _ = s.emu.Write([]byte("Q\r\n"))
	_, _ = s.emu.Write([]byte("R\r\n"))

	if got := s.emu.ScrollbackLen(); got < 2 {
		t.Fatalf("expected both wrapped rows in scrollback, got %d", got)
	}

	before := scrollbackANSI(s.emu, 10)
	if !strings.Contains(before, "ABCDEFGHIJ\r\nKLMNOP") {
		t.Fatalf("width-10 scrollback should preserve the wrap boundary: %q", before)
	}
	storedRows := strings.Count(before, "\r\n")

	// Resize wider must not reflow stored scrollback.
	s.emu.Resize(40, 2)

	if after := scrollbackANSI(s.emu, 10); after != before {
		t.Fatalf("resize changed width-10 scrollback:\nbefore=%q\n after=%q", before, after)
	}
	revealed := scrollbackANSI(s.emu, 40)
	if !strings.Contains(revealed, "ABCDEFGHIJ\r\nKLMNOP") {
		t.Fatalf("width-40 scrollback lost the wrap boundary (unexpected reflow): %q", revealed)
	}
	if strings.Contains(revealed, "ABCDEFGHIJKLMNOP") {
		t.Fatalf("width-40 scrollback merged the wrapped rows (unexpected reflow): %q", revealed)
	}
	if got := strings.Count(revealed, "\r\n"); got != storedRows {
		t.Fatalf("resize changed the stored-row count: got %d, want %d", got, storedRows)
	}
}

// TestCaptureHistoryWidthPlumbing proves captureHistory threads the request Cols
// through to scrollbackANSI as the render width, ignores Rows, and falls back to
// the emulator width when Cols is 0.
func TestCaptureHistoryWidthPlumbing(t *testing.T) {
	s := newConptySession("plumb", "copilot", "", "cmd", 40, 3, false, nil).(*conptySession)
	line := "LEFT" + strings.Repeat(" ", 16) + "RIGHTEDGE\r\n"
	for i := 0; i < 6; i++ {
		_, _ = s.emu.Write([]byte(line))
	}
	if s.emu.ScrollbackLen() == 0 {
		t.Fatal("expected populated scrollback")
	}

	// Cols is the render width (includeScreen=false, scrollback only).
	if ansi, _, _ := s.captureHistory(false, 8, 0); ansi != scrollbackANSI(s.emu, 8) {
		t.Fatalf("captureHistory(false, 8, 0) should equal scrollbackANSI(s.emu, 8)")
	}
	// Rows is accepted but ignored: it must not change the rendered width.
	if ansi, _, _ := s.captureHistory(false, 8, 99); ansi != scrollbackANSI(s.emu, 8) {
		t.Fatalf("captureHistory rows arg must not affect the width-8 output")
	}
	// Cols 0 falls back to the live emulator width.
	if ansi, _, _ := s.captureHistory(false, 0, 0); ansi != scrollbackANSI(s.emu, s.emu.Width()) {
		t.Fatalf("captureHistory(false, 0, 0) should fall back to the emulator width")
	}
}
