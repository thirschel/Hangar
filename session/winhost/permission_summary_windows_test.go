//go:build windows

package winhost

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
)

func TestPermissionToolCallID(t *testing.T) {
	id := "call-123"
	if got := permissionToolCallID(&copilot.PermissionRequestedData{
		PermissionRequest: &copilot.PermissionRequestShell{
			ToolCallID: &id,
		},
	}); got != id {
		t.Fatalf("permissionToolCallID(shell) = %q, want %q", got, id)
	}

	if got := permissionToolCallID(nil); got != "" {
		t.Fatalf("permissionToolCallID(nil) = %q, want empty", got)
	}
}
