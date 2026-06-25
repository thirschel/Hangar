package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV13(t *testing.T) {
	if Version != 13 {
		t.Fatalf("Version = %d, want 13", Version)
	}
}

func TestMCPDetailAndSkillsEventKinds(t *testing.T) {
	if EventKindMCPDetail != "mcp.detail" {
		t.Fatalf("EventKindMCPDetail = %q, want mcp.detail", EventKindMCPDetail)
	}
	if EventKindSkills != "skills" {
		t.Fatalf("EventKindSkills = %q, want skills", EventKindSkills)
	}
}

// TestMCPDetailFrameRoundTrip guards the v13 wire shape both agents implement to:
// a mcp.detail frame carries the full server list with all per-server fields.
func TestMCPDetailFrameRoundTrip(t *testing.T) {
	frame := EventFrame{
		Seq:  3,
		Kind: EventKindMCPDetail,
		MCPServers: []MCPServerInfo{
			{Name: "github", Status: "connected", Transport: "stdio", Source: "user", Tools: []string{"read", "write"}},
			{Name: "broken", Status: "failed", Error: "boom"},
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != EventKindMCPDetail || len(got.MCPServers) != 2 {
		t.Fatalf("round-trip = %+v", got)
	}
	s0 := got.MCPServers[0]
	if s0.Name != "github" || s0.Status != "connected" || s0.Transport != "stdio" ||
		s0.Source != "user" || len(s0.Tools) != 2 || s0.Tools[0] != "read" || s0.Tools[1] != "write" {
		t.Fatalf("server0 round-trip = %+v", s0)
	}
	if got.MCPServers[1].Name != "broken" || got.MCPServers[1].Status != "failed" || got.MCPServers[1].Error != "boom" {
		t.Fatalf("server1 round-trip = %+v", got.MCPServers[1])
	}
	// JSON keys must match the frozen contract exactly.
	if !containsKey(b, "mcpServers") {
		t.Fatalf("expected mcpServers key, got %s", b)
	}
}

func TestSkillsFrameRoundTrip(t *testing.T) {
	frame := EventFrame{
		Seq:  4,
		Kind: EventKindSkills,
		Skills: []SkillInfo{
			{Name: "foo", Description: "does foo", Enabled: true, Source: "project", Path: "/s/foo.md"},
			{Name: "bar"},
		},
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got EventFrame
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != EventKindSkills || len(got.Skills) != 2 {
		t.Fatalf("round-trip = %+v", got)
	}
	if got.Skills[0] != (SkillInfo{Name: "foo", Description: "does foo", Enabled: true, Source: "project", Path: "/s/foo.md"}) {
		t.Fatalf("skill0 = %+v", got.Skills[0])
	}
	if got.Skills[1] != (SkillInfo{Name: "bar"}) {
		t.Fatalf("skill1 = %+v", got.Skills[1])
	}
	if !containsKey(b, "skills") {
		t.Fatalf("expected skills key, got %s", b)
	}
}

// TestDetailFieldsOmittedWhenEmpty guards the additive contract: a non-detail
// frame must not serialize the v13 list fields, so older code paths are unchanged.
func TestDetailFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(EventFrame{Seq: 1, Kind: EventKindIdle})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{"mcpServers", "skills"} {
		if containsKey(b, field) {
			t.Fatalf("expected %q to be omitted on a non-detail frame, got %s", field, b)
		}
	}
}
