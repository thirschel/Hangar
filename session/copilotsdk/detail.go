package copilotsdk

import copilot "github.com/github/copilot-sdk/go"

// MCPServerDetail is a display-safe snapshot of one MCP server's state, captured
// from the SDK event stream for the rich MCP page (v13). It intentionally exposes
// names/status/transport/source/tool-names only — never URLs, commands, args,
// env, headers, or tokens. winhost maps it to proto.MCPServerInfo (this package
// stays free of any proto coupling).
type MCPServerDetail struct {
	Name      string
	Status    string
	Transport string
	Source    string
	Error     string
	Tools     []string
}

// SkillDetail is a display-safe snapshot of one resolved skill, captured from the
// SDK event stream for the rich Skills page (v13). winhost maps it to
// proto.SkillInfo.
type SkillDetail struct {
	Name        string
	Description string
	Enabled     bool
	Source      string
	Path        string
}

// MCPServers returns a copy of the most recent full MCP server detail captured
// from the SDK event stream (servers-loaded / status-changed). The list is
// rebuilt as a whole on every change so callers can replace their view wholesale.
// Returns nil when nothing has been captured yet.
func (s *Session) MCPServers() []MCPServerDetail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.mcpDetail) == 0 {
		return nil
	}
	out := make([]MCPServerDetail, len(s.mcpDetail))
	for i, d := range s.mcpDetail {
		out[i] = d
		out[i].Tools = append([]string(nil), d.Tools...)
	}
	return out
}

// Skills returns a copy of the most recent skills snapshot captured from the SDK
// event stream (skills-loaded). Returns nil when nothing has been captured yet.
func (s *Session) Skills() []SkillDetail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.skills) == 0 {
		return nil
	}
	out := make([]SkillDetail, len(s.skills))
	copy(out, s.skills)
	return out
}

func (s *Session) setMCPTools(tools map[string][]string) {
	s.mu.Lock()
	s.mcpTools = tools
	s.mu.Unlock()
}

// captureMCPServersLoaded rebuilds the full MCP detail list from a servers-loaded
// event, merging in the best-effort configured tool allowlist for each server.
func (s *Session) captureMCPServersLoaded(data *copilot.SessionMCPServersLoadedData) {
	if data == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	detail := make([]MCPServerDetail, 0, len(data.Servers))
	for _, sv := range data.Servers {
		detail = append(detail, mcpServerDetail(sv, s.mcpTools[sv.Name]))
	}
	s.mcpDetail = detail
}

// captureMCPServerStatusChanged updates one server's status in place, preserving
// the loaded-snapshot order. A status for an unknown server (one reported before
// or without a loaded snapshot) is appended best-effort so the detail view still
// reflects it.
func (s *Session) captureMCPServerStatusChanged(data *copilot.SessionMCPServerStatusChangedData) {
	if data == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.mcpDetail {
		if s.mcpDetail[i].Name == data.ServerName {
			s.mcpDetail[i].Status = string(data.Status)
			s.mcpDetail[i].Error = strDeref(data.Error)
			return
		}
	}
	s.mcpDetail = append(s.mcpDetail, MCPServerDetail{
		Name:   data.ServerName,
		Status: string(data.Status),
		Error:  strDeref(data.Error),
		Tools:  append([]string(nil), s.mcpTools[data.ServerName]...),
	})
}

// captureSkillsLoaded replaces the captured skills snapshot from a skills-loaded event.
func (s *Session) captureSkillsLoaded(data *copilot.SessionSkillsLoadedData) {
	if data == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	skills := make([]SkillDetail, 0, len(data.Skills))
	for _, sk := range data.Skills {
		skills = append(skills, SkillDetail{
			Name:        sk.Name,
			Description: sk.Description,
			Enabled:     sk.Enabled,
			Source:      string(sk.Source),
			Path:        strDeref(sk.Path),
		})
	}
	s.skills = skills
}

// mcpServerDetail maps an SDK servers-loaded entry to a display-safe detail,
// attaching the best-effort configured tool allowlist (tools may be nil).
func mcpServerDetail(sv copilot.MCPServersLoadedServer, tools []string) MCPServerDetail {
	d := MCPServerDetail{
		Name:   sv.Name,
		Status: string(sv.Status),
		Error:  strDeref(sv.Error),
		Tools:  append([]string(nil), tools...),
	}
	if sv.Transport != nil {
		d.Transport = string(*sv.Transport)
	}
	if sv.Source != nil {
		d.Source = string(*sv.Source)
	}
	return d
}

func strDeref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
