// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { EventFrame, WorkspaceInfo } from '../../../main/host-client';
import { ChatViewHost } from '../components/ChatViewHost';

function makeWorkspace(overrides: Partial<WorkspaceInfo> = {}): WorkspaceInfo {
  return {
    id: 'ws-1',
    kind: 'rich',
    title: 'My Chat',
    program: 'copilot',
    repoPath: 'C:/repo',
    worktreePath: 'C:/repo',
    branch: 'main',
    sessionName: 'rich-session',
    alive: true,
    autoYes: false,
    added: 0,
    removed: 0,
    createdUnix: 0,
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

describe('ChatViewHost', () => {
  let richFrameCallback: ((data: { session: string; frame: EventFrame }) => void) | undefined;

  beforeEach(() => {
    richFrameCallback = undefined;
    window.cs.openRichStream = vi
      .fn()
      .mockResolvedValue({ attachPipe: 'rich_mock', attachToken: 'token_mock' });
    window.cs.closeRichStream = vi.fn().mockResolvedValue(undefined);
    window.cs.onRichFrame = vi.fn((callback: (data: { session: string; frame: EventFrame }) => void) => {
      richFrameCallback = callback;
      return () => {};
    });
    window.cs.onRichError = vi.fn().mockReturnValue(() => {});
    window.cs.sendMessage = vi.fn().mockResolvedValue(undefined);
    window.cs.setWorkspaceAutoYes = vi.fn().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('opens the rich stream for the chat session and shows the title + nav', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);

    await vi.waitFor(() => expect(window.cs.openRichStream).toHaveBeenCalledWith('rich-session', 0));
    expect(screen.getByRole('heading', { name: 'My Chat' })).toBeInTheDocument();
    // Chat is the only live nav tab; the Phase B tabs are disabled.
    expect(screen.getByRole('button', { name: 'Chat' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'MCP servers' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'All files' })).toBeDisabled();
  });

  it('renders assistant text as full-width plain Markdown (not a bubble)', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: 'Hello ' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.message', text: 'Hello there' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'idle' } });
    });

    const assistant = screen.getByText('Hello there');
    expect(assistant.closest('.chat-msg--assistant')).not.toBeNull();
    expect(assistant.closest('.md')).not.toBeNull(); // rendered through <Markdown>
    expect(assistant.closest('.chat-msg--user')).toBeNull();
    expect(screen.getByText('Turn complete.')).toBeInTheDocument();
  });

  it('renders a completed tool card with server + state', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'tool.start', toolName: 'read_file', mcpServer: 'filesystem', requestId: 't1' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'tool.complete', toolName: 'read_file', mcpServer: 'filesystem', requestId: 't1', status: 'ok' } });
    });

    expect(screen.getByText('read_file')).toBeInTheDocument();
    expect(screen.getByText('filesystem')).toBeInTheDocument();
    expect(screen.getByText('Done')).toBeInTheDocument();
  });

  it('sends a message and shows it optimistically as a right-aligned user bubble', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const textarea = screen.getByPlaceholderText('Message Copilot…');
    fireEvent.change(textarea, { target: { value: 'ping' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(window.cs.sendMessage).toHaveBeenCalledWith('rich-session', 'ping');
    const bubble = screen.getByText('ping');
    expect(bubble.closest('.chat-msg--user')).not.toBeNull();
    expect(bubble.closest('.md')).toBeNull(); // user text is plain, not Markdown
  });

  it('toggles AutoYes optimistically and calls the host', async () => {
    render(<ChatViewHost workspace={makeWorkspace({ autoYes: false })} />);
    await vi.waitFor(() => expect(window.cs.onRichFrame).toHaveBeenCalled());

    const checkbox = screen.getByRole('checkbox', { name: /AutoYes/i });
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);

    expect(window.cs.setWorkspaceAutoYes).toHaveBeenCalledWith('ws-1', true);
    expect(checkbox).toBeChecked();
  });

  it('filters frames for other sessions', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'other-session', frame: { seq: 1, kind: 'assistant.message', text: 'Nope' } });
    });

    expect(screen.queryByText('Nope')).not.toBeInTheDocument();
  });
});
