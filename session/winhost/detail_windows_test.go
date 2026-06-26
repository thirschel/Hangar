//go:build windows

package winhost

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

func TestMCPServerInfosMapping(t *testing.T) {
	details := []copilotsdk.MCPServerDetail{
		{Name: "github", Status: "connected", Transport: "stdio", Source: "user", Tools: []string{"read", "write"}},
		{Name: "broken", Status: "failed", Error: "boom"},
	}
	got := mcpServerInfos(details)
	if len(got) != 2 {
		t.Fatalf("mcpServerInfos len = %d, want 2", len(got))
	}
	g := got[0]
	if g.Name != "github" || g.Status != "connected" || g.Transport != "stdio" || g.Source != "user" || g.Error != "" {
		t.Fatalf("github info = %+v", g)
	}
	if len(g.Tools) != 2 || g.Tools[0] != "read" || g.Tools[1] != "write" {
		t.Fatalf("github tools = %v", g.Tools)
	}
	// Tools must be copied, not aliased to the source slice.
	g.Tools[0] = "mutated"
	if details[0].Tools[0] != "read" {
		t.Fatal("mcpServerInfos aliased the source Tools slice")
	}
	b := got[1]
	if b.Name != "broken" || b.Status != "failed" || b.Error != "boom" || b.Transport != "" || b.Source != "" {
		t.Fatalf("broken info = %+v", b)
	}
	if b.Tools != nil {
		t.Fatalf("broken tools should be nil, got %v", b.Tools)
	}
	if mcpServerInfos(nil) != nil {
		t.Fatal("mcpServerInfos(nil) should be nil")
	}
}

func TestPendingMCPServerInfos(t *testing.T) {
	got := pendingMCPServerInfos([]string{"github", "", "docs"})
	if len(got) != 2 {
		t.Fatalf("pendingMCPServerInfos len = %d, want 2 (empty dropped)", len(got))
	}
	for i, want := range []string{"github", "docs"} {
		if got[i].Name != want || got[i].Status != "pending" {
			t.Fatalf("pending info %d = %+v", i, got[i])
		}
	}
	if pendingMCPServerInfos(nil) != nil {
		t.Fatal("pendingMCPServerInfos(nil) should be nil")
	}
	if pendingMCPServerInfos([]string{"", ""}) != nil {
		t.Fatal("all-empty names should yield nil")
	}
}

func TestSkillInfosMapping(t *testing.T) {
	details := []copilotsdk.SkillDetail{
		{Name: "foo", Description: "does foo", Enabled: true, Source: "project", Path: "/s/foo.md"},
		{Name: "bar"},
	}
	got := skillInfos(details)
	if len(got) != 2 {
		t.Fatalf("skillInfos len = %d, want 2", len(got))
	}
	if got[0] != (proto.SkillInfo{Name: "foo", Description: "does foo", Enabled: true, Source: "project", Path: "/s/foo.md"}) {
		t.Fatalf("foo info = %+v", got[0])
	}
	if got[1] != (proto.SkillInfo{Name: "bar"}) {
		t.Fatalf("bar info = %+v", got[1])
	}
	if skillInfos(nil) != nil {
		t.Fatal("skillInfos(nil) should be nil")
	}
}

// TestEmitMCPDetailSnapshot asserts captured detail maps into a single mcp.detail
// frame carrying the full server list (the desktop replaces its page wholesale).
func TestEmitMCPDetailSnapshot(t *testing.T) {
	s := newSDKSession("rich-detail", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPDetailSnapshot([]copilotsdk.MCPServerDetail{
		{Name: "github", Status: "connected", Transport: "stdio", Source: "user", Tools: []string{"read"}},
		{Name: "broken", Status: "failed", Error: "boom"},
	}, nil)

	frames := s.richTranscript(0)
	if len(frames) != 1 {
		t.Fatalf("richTranscript len = %d, want 1", len(frames))
	}
	f := frames[0]
	if f.Kind != proto.EventKindMCPDetail || len(f.MCPServers) != 2 {
		t.Fatalf("mcp.detail frame = %+v", f)
	}
	if f.MCPServers[0].Name != "github" || f.MCPServers[0].Transport != "stdio" ||
		f.MCPServers[0].Source != "user" || len(f.MCPServers[0].Tools) != 1 || f.MCPServers[0].Tools[0] != "read" {
		t.Fatalf("mcp.detail server0 = %+v", f.MCPServers[0])
	}
	if f.MCPServers[1].Name != "broken" || f.MCPServers[1].Status != "failed" || f.MCPServers[1].Error != "boom" {
		t.Fatalf("mcp.detail server1 = %+v", f.MCPServers[1])
	}
}

// TestEmitMCPDetailSnapshotPendingFallback asserts the startup/resume fallback:
// with nothing captured yet, the configured names become a pending snapshot.
func TestEmitMCPDetailSnapshotPendingFallback(t *testing.T) {
	s := newSDKSession("rich-detail", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPDetailSnapshot(nil, []string{"github", "", "docs"})

	frames := s.richTranscript(0)
	if len(frames) != 1 || frames[0].Kind != proto.EventKindMCPDetail || len(frames[0].MCPServers) != 2 {
		t.Fatalf("pending mcp.detail = %+v", frames)
	}
	if frames[0].MCPServers[0].Name != "github" || frames[0].MCPServers[0].Status != "pending" {
		t.Fatalf("pending server = %+v", frames[0].MCPServers[0])
	}
}

func TestEmitMCPDetailSnapshotEmptyNoFrame(t *testing.T) {
	s := newSDKSession("rich-detail", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPDetailSnapshot(nil, nil)
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("empty snapshot should emit no frame, got %+v", frames)
	}
}

func TestEmitSkillsSnapshot(t *testing.T) {
	s := newSDKSession("rich-skills", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitSkillsSnapshot([]copilotsdk.SkillDetail{
		{Name: "foo", Description: "does foo", Enabled: true, Source: "project", Path: "/s/foo.md"},
	})

	frames := s.richTranscript(0)
	if len(frames) != 1 || frames[0].Kind != proto.EventKindSkills || len(frames[0].Skills) != 1 {
		t.Fatalf("skills frame = %+v", frames)
	}
	sk := frames[0].Skills[0]
	if sk.Name != "foo" || !sk.Enabled || sk.Source != "project" || sk.Path != "/s/foo.md" {
		t.Fatalf("skill = %+v", sk)
	}
}

func TestEmitSkillsSnapshotEmptyNoFrame(t *testing.T) {
	s := newSDKSession("rich-skills", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitSkillsSnapshot(nil)
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("empty skills should emit no frame, got %+v", frames)
	}
}

// TestEmitMCPStatusFrames asserts the resume-refresh emitter maps a status list pulled
// via RPC into a per-server status pill plus one mcp.detail snapshot (the path that
// replaces "pending" with real status on resume).
func TestEmitMCPStatusFrames(t *testing.T) {
	s := newSDKSession("rich-resume-mcp", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPStatusFrames([]copilotsdk.MCPServerDetail{
		{Name: "github", Status: "connected", Source: "user", Tools: []string{"read"}},
	})

	frames := s.richTranscript(0)
	var pill, detail *proto.EventFrame
	for i := range frames {
		switch frames[i].Kind {
		case proto.EventKindMCPStatus:
			pill = &frames[i]
		case proto.EventKindMCPDetail:
			detail = &frames[i]
		}
	}
	if pill == nil || pill.MCPServer != "github" || pill.Status != "connected" {
		t.Fatalf("expected a connected mcp.status pill, got %+v", frames)
	}
	if detail == nil || len(detail.MCPServers) != 1 || detail.MCPServers[0].Name != "github" ||
		detail.MCPServers[0].Status != "connected" {
		t.Fatalf("expected a connected mcp.detail snapshot, got %+v", frames)
	}
}

// TestEmitMCPStatusFramesEmptyNoFrame asserts an empty status list emits nothing.
func TestEmitMCPStatusFramesEmptyNoFrame(t *testing.T) {
	s := newSDKSession("rich-resume-mcp", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitMCPStatusFrames(nil)
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("empty status list should emit no frame, got %+v", frames)
	}
}

// TestMCPStatusSignature asserts the resume-poll fingerprint is order-independent and
// changes when any server's status or error changes (so redundant emits are suppressed
// but real transitions are not).
func TestMCPStatusSignature(t *testing.T) {
	a := []copilotsdk.MCPServerDetail{{Name: "a", Status: "pending"}, {Name: "b", Status: "connected"}}
	b := []copilotsdk.MCPServerDetail{{Name: "b", Status: "connected"}, {Name: "a", Status: "pending"}}
	if mcpStatusSignature(a) != mcpStatusSignature(b) {
		t.Fatal("signature should be order-independent")
	}
	c := []copilotsdk.MCPServerDetail{{Name: "a", Status: "connected"}, {Name: "b", Status: "connected"}}
	if mcpStatusSignature(a) == mcpStatusSignature(c) {
		t.Fatal("signature should change when a status changes")
	}
}

func TestEmitInstructionsSnapshot(t *testing.T) {
	s := newSDKSession("rich-instr", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitInstructionsSnapshot([]copilotsdk.InstructionDetail{
		{
			Label:       "Repo instructions",
			SourcePath:  ".github/copilot-instructions.md",
			Type:        "repository",
			Location:    "repository",
			Description: "House style",
			ApplyTo:     []string{"**/*.go"},
			Content:     "Always run gofmt.",
		},
	})

	frames := s.richTranscript(0)
	if len(frames) != 1 || frames[0].Kind != proto.EventKindInstructions || len(frames[0].Instructions) != 1 {
		t.Fatalf("instructions frame = %+v", frames)
	}
	in := frames[0].Instructions[0]
	if in.Label != "Repo instructions" || in.SourcePath != ".github/copilot-instructions.md" ||
		in.Type != "repository" || in.Location != "repository" || in.Description != "House style" ||
		in.Content != "Always run gofmt." || len(in.ApplyTo) != 1 || in.ApplyTo[0] != "**/*.go" {
		t.Fatalf("instruction = %+v", in)
	}
}

func TestEmitInstructionsSnapshotEmptyNoFrame(t *testing.T) {
	s := newSDKSession("rich-instr", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitInstructionsSnapshot(nil)
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("empty instructions should emit no frame, got %+v", frames)
	}
}

func TestEmitAgentsSnapshot(t *testing.T) {
	s := newSDKSession("rich-agents", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitAgentsSnapshot([]copilotsdk.AgentDetail{
		{
			Name:           "reviewer",
			DisplayName:    "Code Reviewer",
			Description:    "Reviews diffs",
			Model:          "gpt-5",
			Path:           "/a/reviewer.md",
			Source:         "user",
			Skills:         []string{"pdf"},
			Tools:          []string{"read"},
			MCPServerNames: []string{"github"},
			UserInvocable:  true,
		},
	})

	frames := s.richTranscript(0)
	if len(frames) != 1 || frames[0].Kind != proto.EventKindAgents || len(frames[0].Agents) != 1 {
		t.Fatalf("agents frame = %+v", frames)
	}
	a := frames[0].Agents[0]
	if a.Name != "reviewer" || a.DisplayName != "Code Reviewer" || a.Model != "gpt-5" || a.Source != "user" ||
		!a.UserInvocable || len(a.Skills) != 1 || len(a.Tools) != 1 || len(a.MCPServerNames) != 1 {
		t.Fatalf("agent = %+v", a)
	}
}

func TestEmitAgentsSnapshotEmptyNoFrame(t *testing.T) {
	s := newSDKSession("rich-agents", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.emitAgentsSnapshot(nil)
	if frames := s.richTranscript(0); len(frames) != 0 {
		t.Fatalf("empty agents should emit no frame, got %+v", frames)
	}
}

// TestTranslateAndEmitPillBarUnchanged proves the additive mcp.detail emission
// leaves the existing per-server mcp.status pill stream untouched, and that a
// skills-loaded event routes to emitSkills. emitSkills now pulls via
// RPC.Skills.Discover; the unstarted test session has no client, so the pull
// errors and emits no frame (rather than falling through to a generic frame).
func TestTranslateAndEmitPillBarUnchanged(t *testing.T) {
	s := newSDKSession("rich-route", "copilot", t.TempDir(), "", false, "", "", "", "", nil, nil)
	defer s.close()

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{Servers: []copilot.MCPServersLoadedServer{
		{Name: "github", Status: copilot.MCPServerStatusConnected},
		{Name: "docs", Status: copilot.MCPServerStatusPending},
	}}})
	frames := s.richTranscript(0)
	if len(frames) != 2 {
		t.Fatalf("MCP loaded should emit 2 mcp.status pills (mcp.detail no-op), got %d: %+v", len(frames), frames)
	}
	for _, f := range frames {
		if f.Kind != proto.EventKindMCPStatus {
			t.Fatalf("expected mcp.status pill, got %+v", f)
		}
	}

	s.onSDKEvent(copilot.SessionEvent{Data: &copilot.SessionSkillsLoadedData{Skills: []copilot.SkillsLoadedSkill{
		{Name: "foo", Source: copilot.SkillSourceProject},
	}}})
	if frames := s.richTranscript(0); len(frames) != 2 {
		t.Fatalf("skills loaded must not emit a frame without captured skills, got %+v", frames)
	}
}

func TestIsSkillsEvent(t *testing.T) {
	if !isSkillsEvent(copilot.SessionEvent{Data: &copilot.SessionSkillsLoadedData{}}) {
		t.Fatal("skills-loaded event should be detected")
	}
	if isSkillsEvent(copilot.SessionEvent{Data: &copilot.SessionMCPServersLoadedData{}}) {
		t.Fatal("MCP event should not be a skills event")
	}
	if isSkillsEvent(copilot.SessionEvent{Data: &copilot.AssistantMessageData{}}) {
		t.Fatal("assistant message should not be a skills event")
	}
}
