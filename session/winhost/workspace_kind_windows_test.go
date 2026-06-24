//go:build windows

package winhost

import (
	"testing"

	"hangar/session/winhost/proto"
)

func TestWorkspaceKindOrTerminal(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"", proto.WorkspaceKindTerminal},                          // migrated: pre-Kind records default to terminal
		{proto.WorkspaceKindTerminal, proto.WorkspaceKindTerminal}, // explicit terminal
		{proto.WorkspaceKindRich, proto.WorkspaceKindRich},         // rich
		{"something-unknown", proto.WorkspaceKindTerminal},         // unknown values fall back to terminal
	}
	for _, c := range cases {
		if got := (&workspace{Kind: c.kind}).kindOrTerminal(); got != c.want {
			t.Errorf("kindOrTerminal(%q) = %q, want %q", c.kind, got, c.want)
		}
	}
}
