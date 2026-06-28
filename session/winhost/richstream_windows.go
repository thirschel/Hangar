//go:build windows

package winhost

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

type richSub struct {
	ch chan []byte
}

var snapshotPullTimeout = 12 * time.Second

func (s *sdkSession) onSDKEvent(ev copilot.SessionEvent) {
	if s.bufferMCPStartupEvent(ev) {
		return
	}
	s.translateAndEmit(ev)
}

func (s *sdkSession) translateAndEmit(ev copilot.SessionEvent) {
	// MCP status events fan out to one frame per server, so they are handled here
	// rather than in sdkEventFrame (which maps one event to a single frame). Each
	// MCP load/status change is additionally followed by a single mcp.detail
	// snapshot of the full server list, which the desktop replaces wholesale.
	switch data := ev.Data.(type) {
	case *copilot.SessionMCPServersLoadedData:
		for _, sv := range data.Servers {
			s.emitFrame(proto.EventFrame{
				Kind:      proto.EventKindMCPStatus,
				MCPServer: sv.Name,
				Status:    string(sv.Status),
				Error:     stringPtrValue(sv.Error),
			})
		}
		s.emitMCPDetail()
		return
	case *copilot.SessionMCPServerStatusChangedData:
		s.emitFrame(proto.EventFrame{
			Kind:      proto.EventKindMCPStatus,
			MCPServer: data.ServerName,
			Status:    string(data.Status),
			Error:     stringPtrValue(data.Error),
		})
		s.emitMCPDetail()
		return
	case *copilot.SessionSkillsLoadedData:
		// Dispatch off the SDK's single serial event goroutine: emitSkills makes a
		// blocking DiscoverSkills RPC, and the read loop that delivers that RPC's
		// response is the SAME goroutine running this handler. Calling it inline
		// stalls all further event delivery and can deadlock if the SDK's event
		// queue fills during the round-trip. Mirrors refreshSessionState,
		// which already offloads its blocking pulls to a goroutine. [#7]
		go func() {
			defer recoverGoroutine("winhost.emitSkills")
			s.emitSkills(s.ctx)
		}()
		return
	case *copilot.SessionUsageInfoData:
		s.emitUsage(data)
		return
	case *copilot.AssistantUsageData:
		cur, lim, known := s.sess.Usage()
		if known {
			s.emitFrame(usageFrame(cur, lim, s.sess.CurrentModel(), s.sess.AicUnits()))
		}
		return
	}
	frame, ok := sdkEventFrame(ev)
	if !ok {
		return
	}
	s.emitFrame(frame)
}

func isMCPStatusEvent(ev copilot.SessionEvent) bool {
	switch ev.Data.(type) {
	case *copilot.SessionMCPServersLoadedData, *copilot.SessionMCPServerStatusChangedData:
		return true
	default:
		return false
	}
}

func isSkillsEvent(ev copilot.SessionEvent) bool {
	_, ok := ev.Data.(*copilot.SessionSkillsLoadedData)
	return ok
}

// emitMCPDetail emits one mcp.detail snapshot of the full current MCP server list
// (v13). The desktop replaces its MCP page wholesale on each frame. Before the SDK
// reports real server status (startup/resume), it falls back to a pending list
// built from the configured server names so the page populates immediately.
func (s *sdkSession) emitMCPDetail() {
	s.emitMCPDetailSnapshot(s.sess.MCPServers(), s.sess.MCPServerNames())
}

// emitMCPDetailSnapshot maps captured MCP detail into a single mcp.detail frame,
// falling back to a pending list from the configured server names when nothing has
// been captured yet (startup/resume). Split from emitMCPDetail so the mapping is
// unit-testable without driving a live copilotsdk session.
func (s *sdkSession) emitMCPDetailSnapshot(details []copilotsdk.MCPServerDetail, names []string) {
	servers := mcpServerInfos(details)
	if len(servers) == 0 {
		servers = pendingMCPServerInfos(names)
	}
	if len(servers) == 0 {
		return
	}
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindMCPDetail, MCPServers: servers})
}

// emitMCPStatusFrames emits one status pill per server plus a full mcp.detail snapshot
// from a status list pulled via RPC (the resume-refresh path) rather than received as
// a live event. Mirrors how a live servers-loaded event is translated. A no-op on an
// empty list.
func (s *sdkSession) emitMCPStatusFrames(details []copilotsdk.MCPServerDetail) {
	if len(details) == 0 {
		return
	}
	for _, d := range details {
		s.emitFrame(proto.EventFrame{
			Kind:      proto.EventKindMCPStatus,
			MCPServer: d.Name,
			Status:    d.Status,
			Error:     d.Error,
		})
	}
	s.emitMCPDetailSnapshot(details, nil)
}

// refreshSessionState proactively pulls live session state so the MCP / context / AIC
// panes populate without waiting for the first turn — on both a fresh start and a
// resume. The copilot CLI connects MCP servers and computes context lazily/
// asynchronously (and on resume the replayed transcript doesn't carry them), so this
// seeds usage immediately and polls the MCP server list (the same approach the SDK's
// own e2e uses) until every server settles. Runs in its own goroutine, cancelable via
// the session ctx.
func (s *sdkSession) refreshSessionState() {
	go func() {
		defer recoverGoroutine("winhost.refreshSessionState")
		ctx := s.ctx
		// AIC is persisted and available immediately; the context window may be null
		// until the runtime initializes (then the first turn's usage_info event fills it).
		s.emitSessionUsage(ctx)
		s.pollMCPStatus(ctx)
	}()
}

// emitSessionUsage pulls accumulated AIC + the current context window and emits one
// usage frame, so AIC (and context, when available) show without a turn. A no-op when
// neither is available (the desktop guards a zero token limit / zero AIC).
func (s *sdkSession) emitSessionUsage(ctx context.Context) {
	aic, aicKnown, err := s.sess.UsageMetrics(ctx)
	if err != nil {
		s.logf("SDK usage pull failed for session %q: %v", s.name, err)
	}
	cur, limit, ctxKnown, err := s.sess.ContextWindow(ctx)
	if err != nil {
		s.logf("SDK context pull failed for session %q: %v", s.name, err)
	}
	if !aicKnown && !ctxKnown {
		return
	}
	s.logf("SDK usage for session %q: aic=%.4f tokens=%d/%d ctxKnown=%v", s.name, aic, cur, limit, ctxKnown)
	s.emitUsageSnapshot(cur, limit, s.sess.CurrentModel(), aic)
}

// pollMCPStatus polls RPC.MCP.List until every server reaches a terminal status (or a
// bounded deadline / session cancel), emitting an MCP snapshot whenever the status set
// changes. MCP servers connect asynchronously after start/resume, so a single pull would
// still show "pending"; polling flips the page to connected without a turn.
func (s *sdkSession) pollMCPStatus(ctx context.Context) {
	const (
		interval = 1500 * time.Millisecond
		maxWait  = 45 * time.Second
	)
	deadline := time.Now().Add(maxWait)
	for {
		if s.refreshMCPStatus(ctx) || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// refreshMCPStatus pulls the live MCP status once, emits it when it changed since the
// last emit, and reports whether every server has settled (no server still "pending").
// Pull errors and nil/empty lists are treated as transient so polling can observe
// the first real status list; pollMCPStatus's deadline bounds genuinely empty sessions.
func (s *sdkSession) refreshMCPStatus(ctx context.Context) (settled bool) {
	details, err := s.sdkMCPStatus(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logf("SDK MCP status pull failed for session %q: %v", s.name, err)
		}
		return false
	}
	if len(details) == 0 {
		return false
	}
	if sig := mcpStatusSignature(details); sig != s.lastMCPSig {
		s.lastMCPSig = sig
		s.logf("SDK MCP status for session %q: %d server(s)", s.name, len(details))
		s.emitMCPStatusFrames(details)
	}
	for _, d := range details {
		if d.Status == "pending" {
			return false
		}
	}
	return true
}

func (s *sdkSession) sdkMCPStatus(ctx context.Context) ([]copilotsdk.MCPServerDetail, error) {
	if s.mcpStatusFn != nil {
		return s.mcpStatusFn(ctx)
	}
	return s.sess.MCPStatus(ctx)
}

// mcpStatusSignature is a stable, order-independent fingerprint of a status list, used
// to suppress redundant resume-poll emissions (which would otherwise bloat the replay
// log with identical frames while servers are still connecting).
func mcpStatusSignature(details []copilotsdk.MCPServerDetail) string {
	parts := make([]string, 0, len(details))
	for _, d := range details {
		parts = append(parts, d.Name+"\x00"+d.Status+"\x00"+d.Error)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// emitSkills pulls the skills available to the session (RPC.Skills.Discover) and
// emits one skills snapshot frame. The desktop replaces its Skills page wholesale.
// Like instructions and agents, skills are discovered via an RPC pull rather than
// the session-scoped skills-loaded event (which did not surface ~/.copilot/skills),
// so this is emitted once on stream start and re-emitted when a skills-loaded event
// signals a change. The SDK API is experimental, so a pull failure is logged and
// skipped rather than breaking the stream.
func (s *sdkSession) emitSkills(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, snapshotPullTimeout)
	defer cancel()
	details, err := s.sdkDiscoverSkills(ctx)
	if err != nil {
		s.logf("SDK skills pull failed for session %q: %v", s.name, err)
		return
	}
	s.logf("SDK skills discovered for session %q: %d skill(s)", s.name, len(details))
	s.emitSkillsSnapshot(details)
}

func (s *sdkSession) sdkDiscoverSkills(ctx context.Context) ([]copilotsdk.SkillDetail, error) {
	if s.skillsFn != nil {
		return s.skillsFn(ctx)
	}
	return s.sess.DiscoverSkills(ctx)
}

// emitSkillsSnapshot maps a skills snapshot into a single skills frame. Split from
// emitSkills so the mapping is unit-testable without a live copilotsdk session.
func (s *sdkSession) emitSkillsSnapshot(details []copilotsdk.SkillDetail) {
	skills := skillInfos(details)
	if len(skills) == 0 {
		return
	}
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindSkills, Skills: skills})
}

// emitInstructions pulls the custom-instruction sources the SDK loaded for this
// session (RPC.Instructions.GetSources) and emits one instructions snapshot frame
// (v23). The desktop replaces its Instructions page wholesale. Instructions arrive
// via an RPC pull rather than an event, so this is emitted once on stream start.
// The SDK API is experimental, so a pull failure is logged and skipped rather than
// breaking the stream.
func (s *sdkSession) emitInstructions(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, snapshotPullTimeout)
	defer cancel()
	details, err := s.sdkInstructions(ctx)
	if err != nil {
		s.logf("SDK instructions pull failed for session %q: %v", s.name, err)
		return
	}
	// Diagnostic: surface exactly what the SDK discovered (count + per-source
	// type/location/path) so an empty Instructions page is debuggable from host.log.
	s.logf("SDK instructions discovered for session %q: %d source(s)", s.name, len(details))
	for _, d := range details {
		s.logf("  instruction: type=%q location=%q path=%q label=%q", d.Type, d.Location, d.SourcePath, d.Label)
	}
	s.emitInstructionsSnapshot(details)
}

func (s *sdkSession) sdkInstructions(ctx context.Context) ([]copilotsdk.InstructionDetail, error) {
	if s.instructionsFn != nil {
		return s.instructionsFn(ctx)
	}
	return s.sess.Instructions(ctx)
}

// emitInstructionsSnapshot maps an instructions snapshot into a single instructions
// frame. Split from emitInstructions so the mapping is unit-testable without a live
// copilotsdk session.
func (s *sdkSession) emitInstructionsSnapshot(details []copilotsdk.InstructionDetail) {
	instrs := instructionInfos(details)
	if len(instrs) == 0 {
		return
	}
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindInstructions, Instructions: instrs})
}

// emitAgents pulls the custom agents discovered for this session
// (RPC.Agents.Discover) and emits one agents snapshot frame (v24). The desktop
// replaces its Agents page wholesale. Like instructions, agents arrive via an RPC
// pull rather than an event, so this is emitted once on stream start. The SDK API
// is experimental, so a pull failure is logged and skipped.
func (s *sdkSession) emitAgents(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, snapshotPullTimeout)
	defer cancel()
	details, err := s.sdkAgents(ctx)
	if err != nil {
		s.logf("SDK agents pull failed for session %q: %v", s.name, err)
		return
	}
	s.logf("SDK agents discovered for session %q: %d agent(s)", s.name, len(details))
	s.emitAgentsSnapshot(details)
}

func (s *sdkSession) sdkAgents(ctx context.Context) ([]copilotsdk.AgentDetail, error) {
	if s.agentsFn != nil {
		return s.agentsFn(ctx)
	}
	return s.sess.Agents(ctx)
}

// emitAgentsSnapshot maps an agents snapshot into a single agents frame. Split from
// emitAgents so the mapping is unit-testable without a live copilotsdk session.
func (s *sdkSession) emitAgentsSnapshot(details []copilotsdk.AgentDetail) {
	agents := agentInfos(details)
	if len(agents) == 0 {
		return
	}
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindAgents, Agents: agents})
}

// emitUsage emits one usage frame (v14) translating an SDK context-usage event onto
// the rich event stream. The token counts come straight from the event, so it is
// correct for both live events and transcript replay (which bypasses the copilotsdk
// capture); the Model is the session's current model, best-effort.
func (s *sdkSession) emitUsage(data *copilot.SessionUsageInfoData) {
	if data == nil {
		return
	}
	s.emitUsageSnapshot(data.CurrentTokens, data.TokenLimit, s.sess.CurrentModel(), s.sess.AicUnits())
}

// emitUsageSnapshot maps context usage + model + AI units into a single usage frame.
// Split from emitUsage so the mapping is unit-testable without a live session.
func (s *sdkSession) emitUsageSnapshot(currentTokens, tokenLimit int64, model string, aic float64) {
	s.emitFrame(usageFrame(currentTokens, tokenLimit, model, aic))
}

func usageFrame(currentTokens, tokenLimit int64, model string, aic float64) proto.EventFrame {
	return proto.EventFrame{
		Kind:          proto.EventKindUsage,
		Model:         model,
		CurrentTokens: int(currentTokens),
		TokenLimit:    int(tokenLimit),
		Aic:           aic,
	}
}

// emitModelFrame emits a single model frame (v18) carrying the session's active
// model plus the persisted reasoning effort and context tier, so a (re)attaching
// desktop restores the model selector after a restart. Called after start()/
// startResumed(); a no-op when no model is known yet (e.g. a fresh chat that has
// not selected one) so a brand-new session never emits a blank selection.
func (s *sdkSession) emitModelFrame() {
	model := s.sess.CurrentModel()
	if model == "" {
		return
	}
	s.emitFrame(proto.EventFrame{
		Kind:        proto.EventKindModel,
		Model:       model,
		Effort:      s.effort,
		ContextTier: s.contextTier,
	})
}

func mcpServerInfos(details []copilotsdk.MCPServerDetail) []proto.MCPServerInfo {
	if len(details) == 0 {
		return nil
	}
	out := make([]proto.MCPServerInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.MCPServerInfo{
			Name:      d.Name,
			Status:    d.Status,
			Transport: d.Transport,
			Source:    d.Source,
			Error:     d.Error,
			Tools:     append([]string(nil), d.Tools...),
		})
	}
	return out
}

func pendingMCPServerInfos(names []string) []proto.MCPServerInfo {
	out := make([]proto.MCPServerInfo, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		out = append(out, proto.MCPServerInfo{Name: name, Status: "pending"})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func skillInfos(details []copilotsdk.SkillDetail) []proto.SkillInfo {
	if len(details) == 0 {
		return nil
	}
	out := make([]proto.SkillInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.SkillInfo{
			Name:        d.Name,
			Description: d.Description,
			Enabled:     d.Enabled,
			Source:      d.Source,
			Path:        d.Path,
		})
	}
	return out
}

func instructionInfos(details []copilotsdk.InstructionDetail) []proto.InstructionInfo {
	if len(details) == 0 {
		return nil
	}
	out := make([]proto.InstructionInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.InstructionInfo{
			Label:       d.Label,
			SourcePath:  d.SourcePath,
			Type:        d.Type,
			Location:    d.Location,
			Description: d.Description,
			ApplyTo:     append([]string(nil), d.ApplyTo...),
			Content:     d.Content,
		})
	}
	return out
}

func agentInfos(details []copilotsdk.AgentDetail) []proto.AgentInfo {
	if len(details) == 0 {
		return nil
	}
	out := make([]proto.AgentInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.AgentInfo{
			Name:           d.Name,
			DisplayName:    d.DisplayName,
			Description:    d.Description,
			Model:          d.Model,
			Path:           d.Path,
			Source:         d.Source,
			Skills:         append([]string(nil), d.Skills...),
			Tools:          append([]string(nil), d.Tools...),
			MCPServerNames: append([]string(nil), d.MCPServerNames...),
			UserInvocable:  d.UserInvocable,
		})
	}
	return out
}

func modelInfos(details []copilotsdk.ModelDetail) []proto.ModelInfo {
	if len(details) == 0 {
		return nil
	}
	out := make([]proto.ModelInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.ModelInfo{
			ID:               d.ID,
			Name:             d.Name,
			SupportedEfforts: d.SupportedEfforts,
			DefaultEffort:    d.DefaultEffort,
		})
	}
	return out
}

func (s *sdkSession) emitFrame(frame proto.EventFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.richSeq++
	frame.Seq = s.richSeq
	s.richLog = append(s.richLog, frame)
	b, err := json.Marshal(frame)
	if err != nil {
		return
	}
	for sub := range s.richSubs {
		select {
		case sub.ch <- b:
		default:
		}
	}
}

// turnDangling reports whether the rich log ends mid-turn -- it folds the log
// exactly like the desktop's `turnInProgress`: a "working" frame sets the turn
// active, a terminal frame (assistant.message / idle / error) clears it, and every
// other kind (tool.complete, usage, model, mcp.*, *.resolved, title, …) is ignored.
// A resumed transcript that was interrupted before its terminal frame (e.g. the
// daemon was killed mid-turn) ends active => dangling.
func turnDangling(log []proto.EventFrame) bool {
	active := false
	for _, f := range log {
		switch f.Kind {
		case proto.EventKindAssistantDelta,
			proto.EventKindReasoning,
			proto.EventKindReasoningDelta,
			proto.EventKindToolStart,
			proto.EventKindPermissionRequest,
			proto.EventKindUserInputRequest:
			active = true
		case proto.EventKindAssistantMessage,
			proto.EventKindIdle,
			proto.EventKindError:
			active = false
		}
	}
	return active
}

// emitIdleIfDangling settles a turn left dangling in the rich log by emitting a
// synthetic idle frame, so a session revived from an interrupted transcript
// presents as idle (not stuck "running"). Called once after the resume replay,
// when the session is idle by definition; a no-op when the turn already ended.
// The synthetic idle carries no timestamp, so the desktop renders the neutral
// "Turn complete." marker and re-enables the composer.
func (s *sdkSession) emitIdleIfDangling() {
	s.mu.Lock()
	dangling := turnDangling(s.richLog)
	s.mu.Unlock()
	if dangling {
		s.emitFrame(proto.EventFrame{Kind: proto.EventKindIdle})
	}
}

func sdkEventFrame(ev copilot.SessionEvent) (proto.EventFrame, bool) {
	switch data := ev.Data.(type) {
	case *copilot.AssistantMessageData:
		return proto.EventFrame{Kind: proto.EventKindAssistantMessage, Text: data.Content}, true
	case *copilot.AssistantMessageDeltaData:
		return proto.EventFrame{Kind: proto.EventKindAssistantDelta, Text: data.DeltaContent}, true
	case *copilot.AssistantReasoningDeltaData:
		// Incremental reasoning chunk (v19): forward each delta so the desktop can grow
		// the "thinking" block live; the *copilot.AssistantReasoningData case below is the
		// finalizer carrying the complete block.
		return proto.EventFrame{Kind: proto.EventKindReasoningDelta, Text: data.DeltaContent}, true
	case *copilot.AssistantReasoningData:
		return proto.EventFrame{Kind: proto.EventKindReasoning, Text: data.Content}, true
	case *copilot.ToolExecutionStartData:
		return proto.EventFrame{
			Kind:       proto.EventKindToolStart,
			ToolCallID: data.ToolCallID,
			ToolName:   toolStartName(data),
			ToolArgs:   summarizeToolArgs(data.Arguments),
			MCPServer:  stringPtrValue(data.MCPServerName),
		}, true
	case *copilot.ToolExecutionCompleteData:
		return proto.EventFrame{
			Kind:       proto.EventKindToolComplete,
			ToolCallID: data.ToolCallID,
			ToolName:   toolCompleteName(data),
			ToolResult: summarizeToolResult(data),
		}, true
	case *copilot.PermissionRequestedData:
		return proto.EventFrame{
			Kind:       proto.EventKindPermissionRequest,
			RequestID:  data.RequestID,
			Question:   permissionSummary(data),
			ToolCallID: permissionToolCallID(data),
			ToolName:   permissionToolName(data),
		}, true
	case *copilot.PermissionCompletedData:
		// The SDK emits this after a permission is answered (live: post-RespondPermission;
		// resume: replayed from Transcript). Translating it dismisses the already-answered
		// card instead of re-showing Approve/Reject after a restart (v18).
		return proto.EventFrame{
			Kind:      proto.EventKindPermissionResolved,
			RequestID: data.RequestID,
			Decision:  permissionDecision(data.Result),
		}, true
	case *copilot.UserInputCompletedData:
		// An answered ask_user request; dismiss the matching prompt UI on resume (v18).
		return proto.EventFrame{
			Kind:      proto.EventKindInputResolved,
			RequestID: data.RequestID,
		}, true
	case *copilot.ElicitationCompletedData:
		// An answered elicitation request; same resolve frame as user_input (v18).
		return proto.EventFrame{
			Kind:      proto.EventKindInputResolved,
			RequestID: data.RequestID,
		}, true
	case *copilot.ExitPlanModeCompletedData:
		// An answered exit-plan-mode request (live: post-RespondExitPlanMode; resume:
		// replayed from Transcript). Dismiss the plan-review card and record the chosen
		// action so a restarted session does not re-show it (v25).
		frame := proto.EventFrame{
			Kind:      proto.EventKindExitPlanModeResolved,
			RequestID: data.RequestID,
			Approved:  boolPtrValue(data.Approved),
		}
		if data.SelectedAction != nil {
			frame.SelectedAction = string(*data.SelectedAction)
		}
		return frame, true
	case *copilot.SessionModelChangeData:
		// A live (or replayed) model switch; carry the new selection so the desktop
		// keeps the model selector in sync (v18).
		return proto.EventFrame{
			Kind:        proto.EventKindModel,
			Model:       data.NewModel,
			Effort:      stringPtrValue(data.ReasoningEffort),
			ContextTier: contextTierPtrValue(data.ContextTier),
		}, true
	case *copilot.SessionTitleChangedData:
		return proto.EventFrame{Kind: proto.EventKindTitle, Title: data.Title}, true
	case *copilot.SessionIdleData:
		frame := proto.EventFrame{Kind: proto.EventKindIdle, Aborted: boolPtrValue(data.Aborted)}
		if !ev.Timestamp.IsZero() {
			frame.Timestamp = ev.Timestamp.UnixMilli()
		}
		return frame, true
	default:
		return proto.EventFrame{}, false
	}
}

func (s *sdkSession) richSubscribe(since uint64) ([]proto.EventFrame, *richSub) {
	sub := &richSub{ch: make(chan []byte, 32)}
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := framesSince(s.richLog, since)
	if s.closed {
		close(sub.ch)
		return snapshot, sub
	}
	s.richSubs[sub] = struct{}{}
	return snapshot, sub
}

func (s *sdkSession) richUnsubscribe(sub *richSub) {
	if sub == nil {
		return
	}
	s.mu.Lock()
	if _, ok := s.richSubs[sub]; ok {
		delete(s.richSubs, sub)
		close(sub.ch)
	}
	s.mu.Unlock()
}

func (s *sdkSession) richSend(ctx context.Context, text string, attachments []string, mode string) error {
	send := s.sendFn
	if send == nil {
		send = s.sess.Send
	}
	err := send(ctx, text, attachments, mode)
	s.noteExitFrame()
	return err
}

func (s *sdkSession) richAbort(ctx context.Context) error {
	err := s.sess.Abort(ctx)
	s.noteExitFrame()
	return err
}

// noteExitFrame emits a single error frame the first time the underlying agent
// process is detected to have exited, so a watching client sees the failure.
// Reopening the stream (OpenRichStream) revives/resumes the session.
func (s *sdkSession) noteExitFrame() {
	if !s.sess.Exited() {
		return
	}
	s.mu.Lock()
	if s.exitedNoted || s.closed {
		s.mu.Unlock()
		return
	}
	s.exitedNoted = true
	s.mu.Unlock()
	s.emitFrame(proto.EventFrame{Kind: proto.EventKindError, Error: "agent process exited; reopen to resume"})
}

func (s *sdkSession) richRespondPermission(ctx context.Context, requestID string, approve bool) error {
	return s.sess.RespondPermission(ctx, requestID, approve)
}

func (s *sdkSession) richRespondUserInput(requestID, answer string, freeform bool) error {
	return s.sess.RespondUserInput(requestID, answer, freeform)
}

func (s *sdkSession) richRespondExitPlanMode(requestID string, approved bool, selectedAction, feedback string) error {
	return s.sess.RespondExitPlanMode(requestID, approved, selectedAction, feedback)
}

func (s *sdkSession) richListModels(ctx context.Context) ([]proto.ModelInfo, error) {
	details, err := s.sess.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return modelInfos(details), nil
}

func (s *sdkSession) richSetModel(ctx context.Context, modelID, effort, contextTier string) error {
	return s.sess.SetModel(ctx, modelID, effort, contextTier)
}

func (s *sdkSession) richListCommands(ctx context.Context) ([]proto.CommandInfo, error) {
	details, err := s.sess.ListCommands(ctx)
	if err != nil {
		return nil, err
	}
	return commandInfos(details), nil
}

func (s *sdkSession) richInvokeCommand(ctx context.Context, name, input string) (*proto.CommandResult, error) {
	res, err := s.sess.InvokeCommand(ctx, name, input)
	if err != nil {
		return nil, err
	}
	return commandResult(res), nil
}

func commandInfos(details []copilotsdk.CommandDetail) []proto.CommandInfo {
	out := make([]proto.CommandInfo, 0, len(details))
	for _, d := range details {
		out = append(out, proto.CommandInfo{
			Name:        d.Name,
			Description: d.Description,
			Kind:        d.Kind,
			Aliases:     append([]string(nil), d.Aliases...),
			InputHint:   d.InputHint,
		})
	}
	return out
}

func commandResult(r copilotsdk.CommandResult) *proto.CommandResult {
	opts := make([]proto.SubcommandOption, 0, len(r.SubcommandOptions))
	for _, o := range r.SubcommandOptions {
		opts = append(opts, proto.SubcommandOption{Name: o.Name, Description: o.Description, Group: o.Group})
	}
	return &proto.CommandResult{
		Kind:              r.Kind,
		Text:              r.Text,
		Markdown:          r.Markdown,
		Prompt:            r.Prompt,
		DisplayPrompt:     r.DisplayPrompt,
		Message:           r.Message,
		SubcommandTitle:   r.SubcommandTitle,
		SubcommandCommand: r.SubcommandCommand,
		SubcommandOptions: opts,
	}
}

func (s *sdkSession) richTranscript(since uint64) []proto.EventFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return framesSince(s.richLog, since)
}

func framesSince(frames []proto.EventFrame, since uint64) []proto.EventFrame {
	out := make([]proto.EventFrame, 0, len(frames))
	for _, frame := range frames {
		if frame.Seq > since {
			out = append(out, frame)
		}
	}
	return out
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// contextTierPtrValue dereferences an SDK context-tier pointer to its wire string
// ("" when unset), used to carry a model change's context tier on the model frame.
func contextTierPtrValue(v *copilot.ContextTier) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

// permissionDecision maps an SDK permission result to the wire decision (v18). Any
// approved* kind (ApproveOnce/ApprovedForSession/ApprovedForLocation) is an
// approval; every other kind (denied*, cancelled) — and a nil result — is a
// rejection, so a resolved card never re-shows Approve/Reject after a restart.
func permissionDecision(result copilot.PermissionResult) string {
	if result == nil {
		return proto.DecisionReject
	}
	switch result.Kind() {
	case copilot.PermissionResultKindApproved,
		copilot.PermissionResultKindApprovedForLocation,
		copilot.PermissionResultKindApprovedForSession:
		return proto.DecisionApprove
	default:
		return proto.DecisionReject
	}
}

func boolPtrValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func toolCompleteName(data *copilot.ToolExecutionCompleteData) string {
	if data == nil || data.ToolDescription == nil {
		return ""
	}
	return data.ToolDescription.Name
}

// toolStartName returns the friendliest available name for a starting tool call,
// keeping it consistent with toolCompleteName: prefer the SDK ToolDescription.Name
// (present for MCP tools), falling back to the raw ToolName for plain tools.
func toolStartName(data *copilot.ToolExecutionStartData) string {
	if data == nil {
		return ""
	}
	if data.ToolDescription != nil && data.ToolDescription.Name != "" {
		return data.ToolDescription.Name
	}
	return data.ToolName
}
