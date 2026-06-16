package winhost

import (
	"errors"
	"fmt"
	"testing"
)

// TestAsVersionMismatch covers the cross-platform helper the TUI uses to give a
// friendly message on protocol skew (P7c). It runs on every platform.
func TestAsVersionMismatch(t *testing.T) {
	vm := &VersionMismatch{HostVersion: 2, ClientVersion: 1}

	if got, ok := AsVersionMismatch(vm); !ok || got.HostVersion != 2 || got.ClientVersion != 1 {
		t.Fatalf("direct: got=%v ok=%v", got, ok)
	}
	if got, ok := AsVersionMismatch(fmt.Errorf("load failed: %w", vm)); !ok || got.HostVersion != 2 {
		t.Fatalf("wrapped: got=%v ok=%v", got, ok)
	}
	if _, ok := AsVersionMismatch(errors.New("some other error")); ok {
		t.Fatal("a non-mismatch error must not be reported as a version mismatch")
	}
	if _, ok := AsVersionMismatch(nil); ok {
		t.Fatal("nil must not be reported as a version mismatch")
	}

	want := "session-host protocol mismatch: host=2 client=1"
	if vm.Error() != want {
		t.Fatalf("Error()=%q want %q", vm.Error(), want)
	}
}
