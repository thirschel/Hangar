// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../../main/host-client';
import { AgentMode } from '../AgentMode';

type AgentModeProps = Parameters<typeof AgentMode>[0];

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

function baseProps(overrides: Partial<AgentModeProps> = {}): AgentModeProps {
  return {
    workspaces: [],
    selectedId: null,
    onSelectChat: vi.fn(),
    onCreateChat: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe('AgentMode', () => {
  it('lists only rich chats and shows the empty state when nothing is selected', () => {
    const workspaces = [
      workspace({ id: 'rich', kind: 'rich', title: 'Rich Chat' }),
      workspace({ id: 'term', kind: 'terminal', title: 'Terminal WS' }),
    ];

    render(<AgentMode {...baseProps({ workspaces })} />);

    expect(screen.getByText('Rich Chat')).toBeInTheDocument();
    expect(screen.queryByText('Terminal WS')).not.toBeInTheDocument();
    expect(screen.getByText(/Select a chat or start a new one/i)).toBeInTheDocument();
  });

  it('hosts the selected rich chat', () => {
    const workspaces = [workspace({ id: 'rich', kind: 'rich', title: 'My Chat' })];

    render(<AgentMode {...baseProps({ workspaces, selectedId: 'rich' })} />);

    // Title appears in both the sidebar row and the chat-view host header.
    expect(screen.getAllByText('My Chat').length).toBeGreaterThan(0);
    expect(screen.getByText(/Conversation coming soon/i)).toBeInTheDocument();
  });
});