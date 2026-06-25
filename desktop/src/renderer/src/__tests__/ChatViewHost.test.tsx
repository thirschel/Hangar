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
    // Chat, Changes and All files are live; MCP servers and Skills are disabled.
    expect(screen.getByRole('button', { name: 'Chat' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Changes' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'All files' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'MCP servers' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Skills' })).toBeDisabled();
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

  it('takes over the middle with the ReviewPanel when the Changes tab is clicked', async () => {
    window.cs.workspaceFiles = vi.fn(async () => [{ path: 'changed.ts', added: 2, removed: 1 }]);
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    fireEvent.click(screen.getByRole('button', { name: 'Changes' }));

    // The Changes page (embedded ReviewPanel) renders and the chat composer is
    // gone -- the page has taken over the middle.
    expect(await screen.findByText('changed.ts')).toBeInTheDocument();
    expect(container.querySelector('.review-panel')).not.toBeNull();
    expect(screen.queryByPlaceholderText('Message Copilot…')).not.toBeInTheDocument();
    expect(window.cs.workspaceFiles).toHaveBeenCalledWith('ws-1');
  });

  it('takes over the middle with the FilesPanel when the All files tab is clicked', async () => {
    window.cs.listDir = vi.fn(async () => [{ name: 'README.md', dir: false }]);
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    fireEvent.click(screen.getByRole('button', { name: 'All files' }));

    expect(await screen.findByText('README.md')).toBeInTheDocument();
    expect(container.querySelector('.files-panel')).not.toBeNull();
    expect(screen.queryByPlaceholderText('Message Copilot…')).not.toBeInTheDocument();
    expect(window.cs.listDir).toHaveBeenCalledWith('C:/repo', '');
  });

  it('keeps the transcript and the open stream when switching tabs and back to Chat', async () => {
    window.cs.workspaceFiles = vi.fn(async () => [{ path: 'changed.ts', added: 2, removed: 1 }]);
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // Seed some transcript history on the Chat page.
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.message', text: 'Hello there' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'idle' } });
    });
    expect(screen.getByText('Hello there')).toBeInTheDocument();

    // Switch to Changes: the transcript leaves the DOM while the page is shown.
    fireEvent.click(screen.getByRole('button', { name: 'Changes' }));
    expect(await screen.findByText('changed.ts')).toBeInTheDocument();
    expect(screen.queryByText('Hello there')).not.toBeInTheDocument();

    // Back to Chat: the history is rebuilt from the still-live stream and the
    // stream was never re-opened (openRichStream stays at a single call).
    fireEvent.click(screen.getByRole('button', { name: 'Chat' }));
    expect(screen.getByText('Hello there')).toBeInTheDocument();
    expect(window.cs.openRichStream).toHaveBeenCalledTimes(1);
    expect(window.cs.closeRichStream).not.toHaveBeenCalled();
  });
});
