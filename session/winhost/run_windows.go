//go:build windows

package winhost

import (
	"os/exec"
	"regexp"
	"strconv"
	"sync"
)

const runBufferCap = 256 << 10

var previewURLRe = regexp.MustCompile(`https?://(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):\d+\S*`)

type runManager struct {
	mu     sync.Mutex
	states map[string]*runState
}

func newRunManager() *runManager {
	return &runManager{states: map[string]*runState{}}
}

// runShell builds the shell invocation for a workspace Run command (e.g.
// `npm run dev`). Unlike the agent launch path, RunCommand legitimately needs a
// real shell (pipes, `&&`, `.cmd`/`.bat` shims), so it is intentionally NOT
// de-shelled. Instead it is confined to this single, clearly-labeled chokepoint.
// It must only ever be reached from an explicit, user-initiated `runs.start`
// action — never auto-fired from persisted state on load/revive (F-33).
func runShell(command string) *exec.Cmd {
	return exec.Command("cmd.exe", "/c", command)
}

func (m *runManager) state(id string) *runState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[id]
	if st == nil {
		st = &runState{exitCode: 0}
		m.states[id] = st
	}
	return st
}

func (m *runManager) existingState(id string) *runState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[id]
}

func (m *runManager) start(id, worktreePath, command string) error {
	return m.state(id).start(worktreePath, command)
}

func (m *runManager) stop(id string) {
	if st := m.existingState(id); st != nil {
		st.stop()
	}
}

func (m *runManager) output(id string, sinceOffset int64) ([]byte, int64, bool, int) {
	st := m.existingState(id)
	if st == nil {
		return nil, 0, false, 0
	}
	return st.output(sinceOffset)
}

func (m *runManager) info(id string) (running bool, previewURL string) {
	st := m.existingState(id)
	if st == nil {
		return false, ""
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.running, st.previewURL
}

func (m *runManager) stopAll() {
	m.mu.Lock()
	states := make([]*runState, 0, len(m.states))
	for _, st := range m.states {
		states = append(states, st)
	}
	m.mu.Unlock()
	for _, st := range states {
		st.stop()
	}
}

type runState struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	running      bool
	exitCode     int
	buf          []byte
	totalWritten int64
	previewURL   string
	seq          uint64
}

func (s *runState) start(worktreePath, command string) error {
	s.mu.Lock()
	if s.running {
		s.stopLocked()
	}
	s.seq++
	seq := s.seq
	s.buf = nil
	s.previewURL = ""
	s.exitCode = 0

	cmd := runShell(command)
	cmd.Dir = worktreePath
	hideConsole(cmd)
	w := runOutputWriter{state: s, seq: seq}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		s.cmd = nil
		s.running = false
		s.exitCode = -1
		s.mu.Unlock()
		return err
	}
	s.cmd = cmd
	s.running = true
	s.mu.Unlock()

	go func() {
		defer recoverGoroutine("run.wait")
		_ = cmd.Wait()
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		s.mu.Lock()
		if s.seq == seq {
			s.running = false
			s.exitCode = exitCode
			s.cmd = nil
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *runState) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *runState) stopLocked() {
	if !s.running || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	pid := s.cmd.Process.Pid
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	hideConsole(kill)
	_ = kill.Run()
	s.running = false
	s.exitCode = -1
	s.cmd = nil
	s.seq++
}

func (s *runState) output(sinceOffset int64) ([]byte, int64, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	earliest := s.totalWritten - int64(len(s.buf))
	if sinceOffset < earliest {
		sinceOffset = earliest
	}
	if sinceOffset > s.totalWritten {
		sinceOffset = s.totalWritten
	}
	start := int(sinceOffset - earliest)
	data := append([]byte(nil), s.buf[start:]...)
	return data, s.totalWritten, s.running, s.exitCode
}

func (s *runState) appendForSeq(seq uint64, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq != seq {
		return
	}
	if len(p) >= runBufferCap {
		s.buf = append(s.buf[:0], p[len(p)-runBufferCap:]...)
	} else {
		s.buf = append(s.buf, p...)
		if over := len(s.buf) - runBufferCap; over > 0 {
			copy(s.buf, s.buf[over:])
			s.buf = s.buf[:runBufferCap]
		}
	}
	s.totalWritten += int64(len(p))
	if s.previewURL == "" {
		if match := previewURLRe.Find(s.buf); match != nil {
			s.previewURL = string(match)
		}
	}
}

type runOutputWriter struct {
	state *runState
	seq   uint64
}

func (w runOutputWriter) Write(p []byte) (int, error) {
	w.state.appendForSeq(w.seq, p)
	return len(p), nil
}
