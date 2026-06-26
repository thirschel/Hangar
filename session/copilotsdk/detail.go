package copilotsdk

import (
	copilot "github.com/github/copilot-sdk/go"
	csrpc "github.com/github/copilot-sdk/go/rpc"
)

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

// InstructionDetail is a display-safe snapshot of one loaded custom-instruction
// source, pulled from the SDK via RPC.Instructions.GetSources for the rich
// Instructions page (v23). winhost maps it to proto.InstructionInfo.
type InstructionDetail struct {
	Label       string
	SourcePath  string
	Type        string // category used for merge logic
	Location    string // where the source lives (UI grouping)
	Description string
	ApplyTo     []string // frontmatter globs; applies only to matching files
	Content     string   // raw instruction file content
}

// instructionDetail maps one SDK InstructionSource to the display-safe
// InstructionDetail: the string-enum Type/Location are flattened, the optional
// Description pointer is dereferenced, and ApplyTo is defensively copied so callers
// never alias the SDK slice.
func instructionDetail(src csrpc.InstructionSource) InstructionDetail {
	d := InstructionDetail{
		Label:      src.Label,
		SourcePath: src.SourcePath,
		Type:       string(src.Type),
		Location:   string(src.Location),
		Content:    src.Content,
		ApplyTo:    append([]string(nil), src.ApplyTo...),
	}
	if src.Description != nil {
		d.Description = *src.Description
	}
	return d
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

// ModelDetail is a display-safe snapshot of one selectable model, returned by
// (*Session).ListModels for the rich model selector (v14). It exposes the id and
// human-readable name plus the model's reasoning-effort options (v16); winhost maps
// it to proto.ModelInfo (this package stays free of any proto coupling).
type ModelDetail struct {
	ID   string
	Name string

	// SupportedEfforts/DefaultEffort (v16) are the model's reasoning-effort options,
	// from the SDK ModelInfo.SupportedReasoningEfforts/DefaultReasoningEffort. Empty
	// when the model has no selectable reasoning effort.
	SupportedEfforts []string
	DefaultEffort    string
}

// CurrentModel returns the session's active model id (best-effort; "" if unknown).
// It is seeded from Config.Model and updated on SessionModelChangeData / SetModel.
func (s *Session) CurrentModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentModel
}

// Usage returns the most recent context-window usage captured from the SDK event
// stream (SessionUsageInfoData). known is false until the first usage_info event.
func (s *Session) Usage() (currentTokens, tokenLimit int64, known bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usageCurrent, s.usageLimit, s.usageKnown
}

// AicUnits returns the accumulated Copilot AI-unit usage reported by assistant
// usage events, converted from nano-AI units to whole AI units.
func (s *Session) AicUnits() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usageAicNano / 1e9
}

func (s *Session) setCurrentModel(model string) {
	if model == "" {
		return
	}
	s.mu.Lock()
	s.currentModel = model
	s.mu.Unlock()
}

// captureUsage records the latest context-window usage from a usage_info event so
// the host can read it back through Usage().
func (s *Session) captureUsage(data *copilot.SessionUsageInfoData) {
	if data == nil {
		return
	}
	s.mu.Lock()
	s.usageCurrent = data.CurrentTokens
	s.usageLimit = data.TokenLimit
	s.usageKnown = true
	s.mu.Unlock()
}

// captureAssistantUsage adds the Copilot nano-AI-unit cost reported by an
// AssistantUsageData event to the session's accumulated usage total.
func (s *Session) captureAssistantUsage(data *copilot.AssistantUsageData) {
	if data == nil || data.CopilotUsage == nil {
		return
	}
	s.mu.Lock()
	s.usageAicNano += data.CopilotUsage.TotalNanoAiu
	s.mu.Unlock()
}

// captureModelChange updates the tracked current model from a model-change event.
func (s *Session) captureModelChange(data *copilot.SessionModelChangeData) {
	if data == nil {
		return
	}
	s.setCurrentModel(data.NewModel)
}
