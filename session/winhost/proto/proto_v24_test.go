package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV24(t *testing.T) {
	if Version < 24 {
		t.Fatalf("Version = %d, want >= 24", Version)
	}
}

// TestV24AgentsRoundTrip guards the v24 wire addition: EventKindAgents carries the
// full AgentInfo list (the custom agents discovered for the session) under the
// "agents" JSON key, replacing the desktop's Agents page wholesale. omitempty keeps
// a frame without agents byte-compatible with a v23 reader.
func TestV24AgentsRoundTrip(t *testing.T) {
	if EventKindAgents != "agents" {
		t.Fatalf("EventKindAgents = %q, want agents", EventKindAgents)
	}

	frame := EventFrame{
		Seq:  9,
		Kind: EventKindAgents,
		Agents: []AgentInfo{
			{
				Name:           "reviewer",
				DisplayName:    "Code Reviewer",
				Description:    "Reviews diffs",
				Model:          "gpt-5",
				Path:           "/home/u/.copilot/agents/reviewer.md",
				Source:         "user",
				Skills:         []string{"pdf"},
				Tools:          []string{"read"},
				MCPServerNames: []string{"github"},
				UserInvocable:  true,
			},
			{Name: "helper", UserInvocable: false},
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal agents: %v", err)
	}
	if !containsKey(b, "agents") {
		t.Fatalf("expected %q key, got %s", "agents", b)
	}

	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal agents: %v", err)
	}
	if got.Kind != EventKindAgents || len(got.Agents) != 2 {
		t.Fatalf("agents frame round-trip = %+v", got)
	}
	a0 := got.Agents[0]
	if a0.Name != "reviewer" || a0.DisplayName != "Code Reviewer" || a0.Description != "Reviews diffs" ||
		a0.Model != "gpt-5" || a0.Path != "/home/u/.copilot/agents/reviewer.md" || a0.Source != "user" ||
		!a0.UserInvocable || len(a0.Skills) != 1 || a0.Skills[0] != "pdf" || len(a0.Tools) != 1 ||
		a0.Tools[0] != "read" || len(a0.MCPServerNames) != 1 || a0.MCPServerNames[0] != "github" {
		t.Fatalf("agent0 = %+v", a0)
	}
	if got.Agents[1].Name != "helper" || got.Agents[1].UserInvocable {
		t.Fatalf("agent1 = %+v", got.Agents[1])
	}

	// omitempty: a frame without agents must NOT emit the key.
	bare, err := json.Marshal(EventFrame{Seq: 10, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal bare: %v", err)
	}
	if containsKey(bare, "agents") {
		t.Fatalf("did not expect %q key on a bare frame, got %s", "agents", bare)
	}
}
