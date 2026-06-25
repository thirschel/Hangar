// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../../main/host-client';
import { ChatSidebar } from '../ChatSidebar';

function chat(overrides: Partial<WorkspaceInfo>): WorkspaceInfo {
  return {
    id: 'c',
    kind: 'rich',
    title: 'Chat',
    program: 'copilot',
    repoPath: 'C:\\src\\Hangar',
    worktreePath: 'C:\\src\\Hangar\\.hangar',
    branch: 'feature',
    sessionName: 'ws_c',
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

describe('ChatSidebar', () => {
  it('renders a row per chat and selects on click', () => {
    const onSelect = vi.fn();
    const chats = [chat({ id: 'a', title: 'Alpha' }), chat({ id: 'b', title: 'Bravo' })];

    render(<ChatSidebar chats={chats} selectedId="a" onSelect={onSelect} onNewChat={vi.fn()} />);

    expect(screen.getByText('Alpha')).toBeInTheDocument();
    expect(screen.getByText('Bravo')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Bravo'));
    expect(onSelect).toHaveBeenCalledWith('b');
  });

  it('invokes onNewChat when the New chat button is clicked', () => {
    const onNewChat = vi.fn();
    render(<ChatSidebar chats={[]} selectedId={null} onSelect={vi.fn()} onNewChat={onNewChat} />);

    fireEvent.click(screen.getByRole('button', { name: /New chat/i }));
    expect(onNewChat).toHaveBeenCalledTimes(1);
  });

  it('shows an empty hint when there are no chats', () => {
    render(<ChatSidebar chats={[]} selectedId={null} onSelect={vi.fn()} onNewChat={vi.fn()} />);
    expect(screen.getByText(/No chats yet/i)).toBeInTheDocument();
  });
});