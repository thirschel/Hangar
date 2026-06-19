//go:build windows

package winhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestHostAutoYesApprovesCopilot is an opt-in end-to-end test: it starts a real
// copilot ConPTY session with host-side AutoYes enabled, asks copilot to delete a
// file (which triggers copilot's "Do you want to run this command?" approval),
// and asserts the file disappears WITHOUT the test ever sending Enter — i.e. the
// host's autoYesLoop approved the prompt on its own.
//
// Requires a logged-in `copilot` on PATH. Enable with:
//
//	$env:COPILOT_AUTOYES_E2E=1; go test ./session/winhost -run AutoYesApproves -v
func TestHostAutoYesApprovesCopilot(t *testing.T) {
	if os.Getenv("COPILOT_AUTOYES_E2E") != "1" {
		t.Skip("set COPILOT_AUTOYES_E2E=1 (and have copilot on PATH) to run")
	}
	if _, err := exec.LookPath("copilot"); err != nil {
		t.Skip("copilot not on PATH")
	}

	work := t.TempDir()
	target := filepath.Join(work, "DELETE_ME.txt")
	if err := os.WriteFile(target, []byte("delete me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newConptySession("e2e", "copilot", work, "cmd", 120, 40, true /* autoYes */, nil).(*conptySession)
	if err := s.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.close()

	// Boot + accept the folder-trust prompt (not an AutoYes approval prompt, so
	// the host won't tap it for us).
	time.Sleep(8 * time.Second)
	_ = s.sendKeys([]byte("\r"))
	time.Sleep(5 * time.Second)

	// Ask copilot to delete the file. This produces an approval prompt that the
	// host's autoYesLoop should approve on its own.
	_ = s.sendKeys([]byte("Run this exact shell command and nothing else: Remove-Item -Force DELETE_ME.txt"))
	time.Sleep(1 * time.Second)
	_ = s.sendKeys([]byte("\r"))

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return // success: file deleted => host auto-approved the command
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("file %s still exists: host AutoYes did not approve the command", target)
}
