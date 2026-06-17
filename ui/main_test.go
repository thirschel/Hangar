package ui

import (
	"claude-squad/log"
	"os"
	"testing"
)

// TestMain initializes the logger before any ui tests run, so methods that log
// (renderer, list bookkeeping) don't dereference a nil logger.
func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}
