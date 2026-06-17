package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTerminalSession implements the TerminalSession interface for testing.
type mockTerminalSession struct {
	startCalled       bool
	startErr          error
	restoreCalled     bool
	restoreErr        error
	closeCalled       bool
	closeErr          error
	captureContent    string
	captureErr        error
	captureOptContent string
	captureOptErr     error
	hasUpdatedVal     bool
	hasPromptVal      bool
	tapEnterCalled    bool
	tapEnterErr       error
	tryApproveCalled  bool
	tryApproveResult  bool
	sendKeysCalled    bool
	sendKeysInput     string
	sendKeysErr       error
	attachCh          chan struct{}
	attachErr         error
	setSizeCalled     bool
	setSizeErr        error
	sessionExists     bool
	detachCalled      bool
	detachErr         error
}

func (m *mockTerminalSession) Start(workDir string) error {
	m.startCalled = true
	return m.startErr
}

func (m *mockTerminalSession) Restore() error {
	m.restoreCalled = true
	return m.restoreErr
}

func (m *mockTerminalSession) Close() error {
	m.closeCalled = true
	return m.closeErr
}

func (m *mockTerminalSession) CapturePaneContent() (string, error) {
	return m.captureContent, m.captureErr
}

func (m *mockTerminalSession) CapturePaneContentWithOptions(start, end string) (string, error) {
	return m.captureOptContent, m.captureOptErr
}

func (m *mockTerminalSession) HasUpdated() (bool, bool) {
	return m.hasUpdatedVal, m.hasPromptVal
}

func (m *mockTerminalSession) TapEnter() error {
	m.tapEnterCalled = true
	return m.tapEnterErr
}

func (m *mockTerminalSession) TryAutoApprove(sessionID string) bool {
	m.tryApproveCalled = true
	return m.tryApproveResult
}

func (m *mockTerminalSession) SetAutoYes(enabled bool) error {
	return nil
}

func (m *mockTerminalSession) SendKeys(keys string) error {
	m.sendKeysCalled = true
	m.sendKeysInput = keys
	return m.sendKeysErr
}

func (m *mockTerminalSession) Attach() (chan struct{}, error) {
	if m.attachErr != nil {
		return nil, m.attachErr
	}
	if m.attachCh == nil {
		m.attachCh = make(chan struct{})
	}
	return m.attachCh, nil
}

func (m *mockTerminalSession) SetDetachedSize(width, height int) error {
	m.setSizeCalled = true
	return m.setSizeErr
}

func (m *mockTerminalSession) DoesSessionExist() bool {
	return m.sessionExists
}

func (m *mockTerminalSession) DetachSafely() error {
	m.detachCalled = true
	return m.detachErr
}

func (m *mockTerminalSession) CheckAndHandleTrustPrompt() bool {
	return false
}

func withTerminalSessionFactory(t *testing.T, factory func(name, program string) TerminalSession) {
	t.Helper()

	original := terminalSessionFactory
	terminalSessionFactory = factory
	t.Cleanup(func() {
		terminalSessionFactory = original
	})
}

func TestInstanceLaunchCommand(t *testing.T) {
	tests := []struct {
		name           string
		program        string
		agentSessionID string
		want           string
	}{
		{
			name:           "copilot with agent session id resumes",
			program:        "copilot",
			agentSessionID: "abc123",
			want:           "copilot --resume=abc123",
		},
		{
			name:           "empty agent session id leaves program unchanged",
			program:        "copilot",
			agentSessionID: "",
			want:           "copilot",
		},
		{
			name:           "non-copilot program leaves program unchanged",
			program:        "claude",
			agentSessionID: "abc123",
			want:           "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &Instance{
				Program:        tt.program,
				AgentSessionID: tt.agentSessionID,
			}

			assert.Equal(t, tt.want, instance.launchCommand())
		})
	}
}

func TestInstanceStartConstructsTerminalSessionWithResumeCommand(t *testing.T) {
	var gotName string
	var gotProgram string
	withTerminalSessionFactory(t, func(name, program string) TerminalSession {
		gotName = name
		gotProgram = program
		return &mockTerminalSession{}
	})

	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "copilot",
	})
	require.NoError(t, err)
	instance.AgentSessionID = "resume-123"

	err = instance.Start(false)
	require.NoError(t, err)

	assert.Equal(t, "test", gotName)
	assert.Equal(t, "copilot --resume=resume-123", gotProgram)
}

func TestFromInstanceDataPausedConstructsTerminalSessionWithResumeCommand(t *testing.T) {
	var gotName string
	var gotProgram string
	withTerminalSessionFactory(t, func(name, program string) TerminalSession {
		gotName = name
		gotProgram = program
		return &mockTerminalSession{}
	})

	instance, err := FromInstanceData(InstanceData{
		Title:          "paused-test",
		Path:           t.TempDir(),
		Status:         Paused,
		Program:        "copilot",
		AgentSessionID: "resume-456",
	})
	require.NoError(t, err)

	assert.Equal(t, "paused-test", gotName)
	assert.Equal(t, "copilot --resume=resume-456", gotProgram)
	assert.NotNil(t, instance.termSession)
}

func TestInstanceDataRoundTripPreservesAgentSessionAndBaseCommit(t *testing.T) {
	withTerminalSessionFactory(t, func(name, program string) TerminalSession {
		return &mockTerminalSession{}
	})

	instance := &Instance{
		Title:          "round-trip",
		Path:           t.TempDir(),
		Status:         Paused,
		Program:        "copilot",
		AgentSessionID: "agent-session-789",
		BaseCommit:     "0123456789abcdef",
	}

	jsonData, err := json.Marshal(instance.ToInstanceData())
	require.NoError(t, err)

	var data InstanceData
	require.NoError(t, json.Unmarshal(jsonData, &data))

	restored, err := FromInstanceData(data)
	require.NoError(t, err)

	assert.Equal(t, "agent-session-789", restored.AgentSessionID)
	assert.Equal(t, "0123456789abcdef", restored.BaseCommit)
}

func TestInstanceDataBackCompatMissingAgentSessionAndBaseCommit(t *testing.T) {
	withTerminalSessionFactory(t, func(name, program string) TerminalSession {
		return &mockTerminalSession{}
	})

	var data InstanceData
	require.NoError(t, json.Unmarshal([]byte(`{"title":"legacy","status":3,"program":"copilot"}`), &data))

	restored, err := FromInstanceData(data)
	require.NoError(t, err)

	assert.Empty(t, restored.AgentSessionID)
	assert.Empty(t, restored.BaseCommit)
}

func TestSetTerminalSession(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	assert.Equal(t, mock, instance.termSession)
}

func TestSessionAlive(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	mock := &mockTerminalSession{sessionExists: true}
	instance.SetTerminalSession(mock)

	assert.True(t, instance.SessionAlive())

	mock.sessionExists = false
	assert.False(t, instance.SessionAlive())
}

func TestPreview_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	content, err := instance.Preview()
	assert.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestPreview_Paused(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Paused
	mock := &mockTerminalSession{captureContent: "should not see this"}
	instance.SetTerminalSession(mock)

	content, err := instance.Preview()
	assert.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestPreview_Started(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Running
	mock := &mockTerminalSession{captureContent: "hello world"}
	instance.SetTerminalSession(mock)

	content, err := instance.Preview()
	assert.NoError(t, err)
	assert.Equal(t, "hello world", content)
}

func TestHasUpdated_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	updated, hasPrompt := instance.HasUpdated()
	assert.False(t, updated)
	assert.False(t, hasPrompt)
}

func TestHasUpdated_Started(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	mock := &mockTerminalSession{hasUpdatedVal: true, hasPromptVal: true}
	instance.SetTerminalSession(mock)

	updated, hasPrompt := instance.HasUpdated()
	assert.True(t, updated)
	assert.True(t, hasPrompt)
}

func TestTapEnter_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)
	instance.AutoYes = true

	// Should not tap enter when not started
	instance.TapEnter()
	assert.False(t, mock.tapEnterCalled)
}

func TestTapEnter_AutoYesDisabled(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.AutoYes = false
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	// Should not tap enter when AutoYes is false
	instance.TapEnter()
	assert.False(t, mock.tapEnterCalled)
}

func TestTapEnter_AutoYesEnabled(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.AutoYes = true
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	instance.TapEnter()
	assert.True(t, mock.tapEnterCalled)
}

func TestAttach_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	ch, err := instance.Attach()
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func TestAttach_Started(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	expectedCh := make(chan struct{})
	mock := &mockTerminalSession{attachCh: expectedCh}
	instance.SetTerminalSession(mock)

	ch, err := instance.Attach()
	assert.NoError(t, err)
	assert.Equal(t, expectedCh, ch)
}

func TestSetPreviewSize_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	err = instance.SetPreviewSize(80, 24)
	assert.Error(t, err)
}

func TestSetPreviewSize_Paused(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Paused
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.SetPreviewSize(80, 24)
	assert.Error(t, err)
	assert.False(t, mock.setSizeCalled)
}

func TestSetPreviewSize_Running(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Running
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.SetPreviewSize(80, 24)
	assert.NoError(t, err)
	assert.True(t, mock.setSizeCalled)
}

func TestSendKeys_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	err = instance.SendKeys("hello")
	assert.Error(t, err)
}

func TestSendKeys_Paused(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Paused
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.SendKeys("hello")
	assert.Error(t, err)
	assert.False(t, mock.sendKeysCalled)
}

func TestSendKeys_Running(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Running
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.SendKeys("hello")
	assert.NoError(t, err)
	assert.True(t, mock.sendKeysCalled)
	assert.Equal(t, "hello", mock.sendKeysInput)
}

func TestSendPrompt_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	err = instance.SendPrompt("hello")
	assert.Error(t, err)
}

func TestSendPrompt_NilSession(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.termSession = nil

	err = instance.SendPrompt("hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "terminal session not initialized")
}

func TestSendPrompt_Success(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.SendPrompt("hello")
	assert.NoError(t, err)
	assert.True(t, mock.sendKeysCalled)
	assert.Equal(t, "hello", mock.sendKeysInput)
	assert.True(t, mock.tapEnterCalled)
}

func TestPreviewFullHistory_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	content, err := instance.PreviewFullHistory()
	assert.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestPreviewFullHistory_Paused(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Paused

	content, err := instance.PreviewFullHistory()
	assert.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestPreviewFullHistory_Running(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	instance.Status = Running
	mock := &mockTerminalSession{captureOptContent: "full history here"}
	instance.SetTerminalSession(mock)

	content, err := instance.PreviewFullHistory()
	assert.NoError(t, err)
	assert.Equal(t, "full history here", content)
}

func TestKill_NotStarted(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	// Kill on an unstarted instance should succeed
	err = instance.Kill()
	assert.NoError(t, err)
}

func TestKill_Started(t *testing.T) {
	instance, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)

	instance.started = true
	mock := &mockTerminalSession{}
	instance.SetTerminalSession(mock)

	err = instance.Kill()
	assert.NoError(t, err)
	assert.True(t, mock.closeCalled)
}

// Compile-time check: mockTerminalSession satisfies TerminalSession.
var _ TerminalSession = (*mockTerminalSession)(nil)
