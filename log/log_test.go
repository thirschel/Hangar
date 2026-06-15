package log

import (
	"strings"
	"testing"
)

func TestLogFileName(t *testing.T) {
	if !strings.HasSuffix(LogFileName(), "claudesquad.log") {
		t.Fatalf("unexpected log file name: %s", LogFileName())
	}
}

func TestCloseBeforeInitializeIsNoOp(t *testing.T) {
	// Close must be safe (and silent) even if Initialize was never called.
	savedInit := initialized
	savedFile := globalLogFile
	defer func() {
		initialized = savedInit
		globalLogFile = savedFile
	}()

	initialized = false
	globalLogFile = nil
	Close() // must not panic
}
