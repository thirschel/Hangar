// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../../main/host-client';
import { AgentMode } from '../AgentMode';

// Mock the shell dock to a sentinel: AgentMode's job is to compose it below the
// chat; CenterTerminal's own behavior (localStorage, shell spawn) is covered by
// CenterTerminal.test.tsx and pulls in xterm/localStorage we don't need here.
vi.mock('../CenterTerminal', () => ({
  CenterTerminal: () => <div data-testid="center-terminal" />,
}));

function workspace(overrides: Partial<WorkspaceInfo>): WorkspaceInfo {
  return {
    id: 'w',
    title: 'Workspace',
    program: 'copilot',
    repoPath: 'C:\\src\\Hangar',
    worktreePath: 'C:\\src\\Hangar\\.hangar',
    branch: 'feature',
    sessionName: 'ws_w',
    alive: true,
    autoYes: false,
    added: 0,
    removed: 0,
    createdUnix: 1,
    lastOutputUnix: 0,
    runCommand: '',
    running: false,
    previewUrl: '',
    busy: false,
    waiting: false,
    regenerating: false,
    shell: 'cmd',
    hasWorktree: true,
    ...overrides,
  };
}

describe('AgentMode', () => {
  it('shows the empty state when no chat is selected', () => {
    render(<AgentMode selectedChat={null} />);

    expect(screen.getByText(/Select a chat or start a new one/i)).toBeInTheDocument();
  });

  it('hosts the selected rich chat', () => {
    const chat = workspace({ id: 'rich', kind: 'rich', title: 'My Chat' });

    render(<AgentMode selectedChat={chat} />);

    // The real ChatView host mounts (section nav) once a chat is selected.
    expect(screen.getByRole('button', { name: 'Chat' })).toBeInTheDocument();
    // The collapsible shell dock sits below the chat.
    expect(screen.getByTestId('center-terminal')).toBeInTheDocument();
    expect(screen.queryByText(/Select a chat or start a new one/i)).not.toBeInTheDocument();
  });

  it('omits the terminal dock in the empty state', () => {
    render(<AgentMode selectedChat={null} />);

    expect(screen.queryByTestId('center-terminal')).not.toBeInTheDocument();
  });
});