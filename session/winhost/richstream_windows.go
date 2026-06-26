//go:build windows

package winhost

import (
	"context"
	"encoding/json"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

type richSub struct {
	ch chan []byte
}

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
		s.emitSkills()
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

// emitSkills emits one skills snapshot of the full current skills list (v13). The
// desktop replaces its Skills page wholesale on each frame. A no-op until the SDK
// has reported a skills-loaded event.
func (s *sdkSession) emitSkills() {
	s.emitSkillsSnapshot(s.sess.Skills())
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

func (s *sdkSession) richSend(ctx context.Context, text string, attachments []string) error {
	send := s.sendFn
	if send == nil {
		send = s.sess.Send
	}
	err := send(ctx, text, attachments)
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
