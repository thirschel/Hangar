//go:build windows

package winhost

import (
	"context"
	"encoding/json"

	copilot "github.com/github/copilot-sdk/go"
	"hangar/session/winhost/proto"
)

type richSub struct {
	ch chan []byte
}

func (s *sdkSession) onSDKEvent(ev copilot.SessionEvent) {
	frame, ok := sdkEventFrame(ev)
	if !ok {
		return
	}

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
	case *copilot.AssistantReasoningData:
		return proto.EventFrame{Kind: proto.EventKindReasoning, Text: data.Content}, true
	case *copilot.ToolExecutionStartData:
		return proto.EventFrame{Kind: proto.EventKindToolStart, ToolName: data.ToolName, MCPServer: stringPtrValue(data.MCPServerName)}, true
	case *copilot.ToolExecutionCompleteData:
		return proto.EventFrame{Kind: proto.EventKindToolComplete, ToolName: toolCompleteName(data)}, true
	case *copilot.PermissionRequestedData:
		return proto.EventFrame{Kind: proto.EventKindPermissionRequest, RequestID: data.RequestID}, true
	case *copilot.SessionTitleChangedData:
		return proto.EventFrame{Kind: proto.EventKindTitle, Title: data.Title}, true
	case *copilot.SessionIdleData:
		return proto.EventFrame{Kind: proto.EventKindIdle, Aborted: boolPtrValue(data.Aborted)}, true
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

func (s *sdkSession) richSend(ctx context.Context, text string) error {
	return s.sess.Send(ctx, text)
}

func (s *sdkSession) richAbort(ctx context.Context) error {
	return s.sess.Abort(ctx)
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
