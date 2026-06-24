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

func TestRichBackend(t *testing.T) {
	cases := []struct {
		name                         string
		reqRich, cfgEnabled, copilot bool
		want                         bool
	}{
		{"default off", false, false, true, false},
		{"client opt-in", true, false, true, true},
		{"server config opt-in", false, true, true, true},
		{"not copilot never rich (req)", true, false, false, false},
		{"not copilot never rich (cfg)", false, true, false, false},
	}
	for _, c := range cases {
		if got := richBackend(c.reqRich, c.cfgEnabled, c.copilot); got != c.want {
			t.Errorf("%s: richBackend(%v,%v,%v) = %v, want %v", c.name, c.reqRich, c.cfgEnabled, c.copilot, got, c.want)
		}
	}
}
