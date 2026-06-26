// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { EventFrame, WorkspaceInfo } from '../../../main/host-client';
import { ChatViewHost, __clearRichFrameCacheForTests, __clearRevealStoreForTests } from '../components/ChatViewHost';

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
    // The rich-frame cache is module-global, so clear it between cases to keep
    // each test's transcript isolated (no frames leaking across mounts/cases).
    __clearRichFrameCacheForTests();
    // The typewriter reveal store is likewise module-global; clear it so reveal
    // progress never leaks across cases.
    __clearRevealStoreForTests();
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
    window.cs.listModels = vi.fn().mockResolvedValue([]);
    window.cs.setModel = vi.fn().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    // The reveal tests install a matchMedia stub; remove it so the default cases
    // (which rely on its absence to snap reveals to full) stay isolated.
    Reflect.deleteProperty(window, 'matchMedia');
  });

  it('opens the rich stream for the chat session and shows the title + nav', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);

    await vi.waitFor(() => expect(window.cs.openRichStream).toHaveBeenCalledWith('rich-session', 0));
    expect(screen.getByRole('heading', { name: 'My Chat' })).toBeInTheDocument();
    // All five sections are now live: Chat, MCP servers, Skills, Changes, All files.
    expect(screen.getByRole('button', { name: 'Chat' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'MCP servers' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Skills' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Changes' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'All files' })).toBeEnabled();
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
    // Graceful fallback for older/replayed idle frames without a timestamp.
    expect(screen.getByText('Turn complete.')).toBeInTheDocument();
  });

  it('renders completed idle frames with the completion time', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const completedAt = new Date('2026-06-25T20:04:00-05:00').getTime();
    const expected = `Completed ${new Date(completedAt).toLocaleTimeString([], {
      hour: 'numeric',
      minute: '2-digit',
    })}`;

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'idle', ts: completedAt } });
    });

    expect(screen.getByText(expected)).toBeInTheDocument();
  });

  it('renders aborted idle frames as canceled with a purple dot', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'idle', aborted: true } });
    });

    const canceled = screen.getByText('Operation canceled by user').closest('.chat-entry--idle');
    expect(canceled).not.toBeNull();
    expect(canceled).toHaveClass('chat-entry--idle--canceled');
    expect(container.querySelector('.chat-entry--idle--canceled .chat-idle-dot--purple')).not.toBeNull();
  });

  it('fades streaming words in but renders the finalized message plainly without remounting', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // Stream the turn in word-by-word deltas: the live message wraps each word
    // in a `.word-fade` span so it can fade in one-after-another.
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: 'Hello ' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.delta', text: 'world' } });
    });

    const streaming = container.querySelector('.chat-msg--assistant');
    expect(streaming).not.toBeNull();
    expect(streaming?.querySelectorAll('.word-fade').length).toBeGreaterThan(0);

    // Finalize with the authoritative full message + idle.
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'assistant.message', text: 'Hello world' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 4, kind: 'idle' } });
    });

    const finalized = container.querySelector('.chat-msg--assistant');
    // Same DOM node: the entry id stayed stable across streaming -> finalize, so
    // React reconciled in place instead of remounting (a remount would re-flash
    // the whole message's word fade-in).
    expect(finalized).toBe(streaming);
    // Finalized message renders plainly -- no per-word spans, no re-animation.
    expect(finalized?.querySelectorAll('.word-fade').length).toBe(0);
    expect(finalized?.textContent).toContain('Hello world');
  });

  it('renders a completed tool call as a clean line with a done dot (no badge)', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'tool.start', toolName: 'read_file', mcpServer: 'filesystem', requestId: 't1' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'tool.complete', toolName: 'read_file', mcpServer: 'filesystem', requestId: 't1' } });
    });

    expect(screen.getByText('read_file')).toBeInTheDocument();
    expect(screen.getByText('filesystem')).toBeInTheDocument();
    // Status is a colored dot now, not a "Running"/"Done" text badge box.
    expect(container.querySelector('.chat-tool__dot--done')).not.toBeNull();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
    expect(screen.queryByText('Running')).not.toBeInTheDocument();
  });

  it('merges tool.start args and tool.complete result into one clean line', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'tool.start', toolName: 'read', toolArgs: 'README.md', requestId: 't1' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'tool.complete', toolName: 'read', toolResult: '150 lines read', requestId: 't1' } });
    });

    // A single clean line: name + args (kept from the start) + result (complete).
    const line = container.querySelector('.chat-tool') as HTMLElement | null;
    expect(line).not.toBeNull();
    const row = line as HTMLElement;
    expect(within(row).getByText('read')).toBeInTheDocument();
    expect(within(row).getByText('README.md')).toBeInTheDocument();
    expect(within(row).getByText('150 lines read')).toBeInTheDocument();
    // A done dot, not a "Done" badge box.
    expect(row.querySelector('.chat-tool__dot--done')).not.toBeNull();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
  });

  it('links an AutoYes permission to a tool that started BEFORE it (read/view ordering)', async () => {
    // The SDK emits tool.start BEFORE permission.requested for reads/view (verified
    // via an SDK spike), so the reducer must link the permission to the already-
    // existing tool entry -- not only when the tool arrives after the permission.
    const { container } = render(<ChatViewHost workspace={makeWorkspace({ autoYes: true })} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'tool.start', toolName: 'view', toolCallId: 'call-1' },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 2,
          kind: 'permission.requested',
          requestId: 'p1',
          toolCallId: 'call-1',
          question: 'Read path: C:/repo/src/app.ts',
          toolName: 'read',
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 3, kind: 'tool.complete', toolCallId: 'call-1' },
      });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 4, kind: 'idle' } });
    });

    const shield = container.querySelector('.chat-tool__perm');
    expect(shield).not.toBeNull();
    expect(within(shield as HTMLElement).getByText('Read path: C:/repo/src/app.ts')).toBeInTheDocument();
    expect(within(shield as HTMLElement).getByText('read')).toBeInTheDocument();
    // The standalone bubble is hidden -- the permission is shown on the tool.
    expect(container.querySelector('.chat-entry--permission')).toBeNull();
  });

  it('links an AutoYes permission to a tool that starts AFTER it (shell ordering)', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace({ autoYes: true })} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'permission.requested',
          requestId: 'p1',
          toolCallId: 'call-2',
          question: 'Run shell command: git status',
          toolName: 'shell',
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'tool.start', toolName: 'bash', toolCallId: 'call-2' },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 3, kind: 'tool.complete', toolCallId: 'call-2' },
      });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 4, kind: 'idle' } });
    });

    const shield = container.querySelector('.chat-tool__perm');
    expect(shield).not.toBeNull();
    expect(within(shield as HTMLElement).getByText('Run shell command: git status')).toBeInTheDocument();
    expect(container.querySelector('.chat-entry--permission')).toBeNull();
  });

  it('keeps AutoYes-off permissions as functional Approve and Reject bubbles', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'permission.requested',
          requestId: 'p1',
          question: 'Run shell command: git status',
          toolName: 'shell',
        },
      });
    });

    expect(container.querySelector('.chat-entry--permission')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Reject' })).toBeEnabled();
  });

  it('shows a "Searching" status with a spinner while a grep tool runs', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'tool.start', toolName: 'grep', toolCallId: 'g1' },
      });
    });

    expect(container.querySelector('.chat-composer__status-label')?.textContent).toBe('Searching');
    expect(container.querySelector('.chat-status-spinner')).not.toBeNull();
  });

  it('derives the status verb from the running tool (view -> Reading)', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'tool.start', toolName: 'view', toolCallId: 'v1' },
      });
    });

    expect(container.querySelector('.chat-composer__status-label')?.textContent).toBe('Reading');
  });

  it('shows "Thinking" while reasoning streams', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'assistant.reasoning.delta', text: 'Considering ' },
      });
    });

    expect(container.querySelector('.chat-composer__status-label')?.textContent).toBe('Thinking');
  });

  it('clears the status (and spinner) once the turn completes (idle)', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'tool.start', toolName: 'bash', toolCallId: 'b1' },
      });
    });
    expect(container.querySelector('.chat-composer__status-label')?.textContent).toBe('Running');

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'tool.complete', toolCallId: 'b1' },
      });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'idle' } });
    });

    expect(container.querySelector('.chat-composer__status-label')).toBeNull();
    expect(container.querySelector('.chat-status-spinner')).toBeNull();
  });

  it('renders reasoning as faded text inside an expanded <details> (no bubble)', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.reasoning', text: 'Thinking about it' } });
    });

    const text = screen.getByText('Thinking about it');
    // Faded class is present and the wrapper is a default-expanded <details>.
    expect(text).toHaveClass('chat-reasoning__text');
    const details = text.closest('details');
    expect(details).not.toBeNull();
    expect(details).toHaveAttribute('open');
    expect(container.querySelector('.chat-entry--reasoning')).toBe(details);
    // The boxed bubble base class (border/background) is dropped -- no bubble.
    expect(details).not.toHaveClass('chat-entry');
  });

  it('streams reasoning deltas into one growing entry finalized by the full frame', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // Three incremental reasoning chunks accumulate into a SINGLE entry...
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.reasoning.delta', text: 'Thinking ' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.reasoning.delta', text: 'about ' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'assistant.reasoning.delta', text: 'it' } });
    });

    expect(container.querySelectorAll('.chat-entry--reasoning')).toHaveLength(1);
    expect(container.querySelector('.chat-reasoning__text')?.textContent).toBe('Thinking about it');

    // ...then the full reasoning frame replaces that same entry's text with the
    // authoritative complete block (still exactly one entry, not multiple).
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 4, kind: 'assistant.reasoning', text: 'Thinking about it carefully.' } });
    });

    expect(container.querySelectorAll('.chat-entry--reasoning')).toHaveLength(1);
    expect(container.querySelector('.chat-reasoning__text')?.textContent).toBe(
      'Thinking about it carefully.',
    );
  });

  it('does not render an inline "Usage updated" entry for a usage frame', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 1, tokenLimit: 100 } });
    });

    // The CLI shows no inline usage row: the snapshot only drives the header, so
    // no transcript entry is pushed (the empty-state placeholder stays visible).
    expect(screen.queryByText('Usage updated')).not.toBeInTheDocument();
    expect(screen.getByText(/Waiting for the agent/)).toBeInTheDocument();
  });

  it('sends a message and shows it optimistically as a right-aligned user bubble', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const textarea = screen.getByPlaceholderText('Message Copilot…');
    fireEvent.change(textarea, { target: { value: 'ping' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(window.cs.sendMessage).toHaveBeenCalledWith('rich-session', 'ping', []);
    const bubble = screen.getByText('ping');
    expect(bubble.closest('.chat-msg--user')).not.toBeNull();
    expect(bubble.closest('.md')).toBeNull(); // user text is plain, not Markdown
  });

  it('forwards picked attachments to sendMessage and reflects them in the bubble', async () => {
    window.cs.pickFiles = vi.fn().mockResolvedValue(['/a/x.go', '/b/y.ts']);
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const textarea = screen.getByPlaceholderText('Message Copilot…');
    fireEvent.change(textarea, { target: { value: 'review these' } });
    // The Upload button is now live: clicking it opens the (mocked) picker and
    // renders a chip per chosen file (by basename) once it resolves.
    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));
    expect(await screen.findByText('x.go')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    // handleSend threads the absolute attachment paths to the daemon call.
    expect(window.cs.sendMessage).toHaveBeenCalledWith('rich-session', 'review these', [
      '/a/x.go',
      '/b/y.ts',
    ]);
    // The optimistic bubble shows the text plus a 📎 basenames summary so the
    // user sees what they sent.
    const bubble = container.querySelector('.chat-msg--user .chat-msg__bubble');
    expect(bubble?.textContent).toContain('review these');
    expect(bubble?.textContent).toContain('x.go');
    expect(bubble?.textContent).toContain('y.ts');
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

  it('keeps the transcript on screen across a stream re-subscribe (no blank/pop-in)', async () => {
    const { unmount } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // Stream an assistant turn into the transcript (delta -> final -> idle).
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: 'Hello ' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.message', text: 'Hello there' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'idle' } });
    });
    expect(screen.getByText('Hello there')).toBeInTheDocument();

    // Simulate the rich stream re-subscribing for the SAME session: unmount +
    // remount re-runs the [workspace.sessionName] effect. The bug blanked the
    // transcript here (setFramesBySeq(new Map())) and only refilled it once the
    // daemon replayed the since=0 snapshot over the wire -- the visible "pop in".
    // The per-session cache seeds framesBySeq synchronously, so the transcript
    // must already be present on remount with NO frames replayed yet.
    unmount();
    richFrameCallback = undefined;
    render(<ChatViewHost workspace={makeWorkspace()} />);

    // Present immediately after remount -- proof it rendered from the cache, not
    // from a replay (no richFrameCallback was invoked after the remount).
    expect(screen.getByText('Hello there')).toBeInTheDocument();

    // And the stream genuinely re-subscribed: a fresh openRichStream at since=0
    // (kept since=0 so a daemon-restart seq reset still replays cleanly).
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());
    expect(window.cs.openRichStream).toHaveBeenCalledTimes(2);
    expect(window.cs.openRichStream).toHaveBeenLastCalledWith('rich-session', 0);
  });

  it('merges a since=0 replay idempotently after re-subscribe without duplicating entries', async () => {
    const { unmount } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.message', text: 'Hello there' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'idle' } });
    });

    // Re-subscribe, then let the daemon replay the same seqs (new frame objects,
    // identical content). They merge by seq over the cache-seeded transcript, so
    // the message is still rendered exactly once -- no flicker, no duplication.
    unmount();
    richFrameCallback = undefined;
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.message', text: 'Hello there' } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'idle' } });
    });

    expect(container.querySelectorAll('.chat-msg--assistant')).toHaveLength(1);
    expect(screen.getByText('Hello there')).toBeInTheDocument();
  });

  it('renders the MCP servers page from an mcp.detail snapshot', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'mcp.detail',
          mcpServers: [
            {
              name: 'filesystem',
              status: 'connected',
              transport: 'stdio',
              source: 'user',
              tools: ['read_file', 'write_file'],
            },
            {
              name: 'github',
              status: 'failed',
              transport: 'http',
              source: 'workspace',
              error: 'authentication required',
            },
          ],
        },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: 'MCP servers' }));

    // Server names, status badges, transports and tools all render on the page.
    expect(screen.getByText('filesystem')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('stdio')).toBeInTheDocument();
    expect(screen.getByText('read_file')).toBeInTheDocument();
    expect(screen.getByText('write_file')).toBeInTheDocument();
    expect(screen.getByText('github')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('authentication required')).toBeInTheDocument();
    // Page takeover: the chat composer is gone and the stream stayed open.
    expect(screen.queryByPlaceholderText('Message Copilot…')).not.toBeInTheDocument();
    expect(window.cs.openRichStream).toHaveBeenCalledTimes(1);
    expect(window.cs.closeRichStream).not.toHaveBeenCalled();
  });

  it('replaces the MCP list wholesale on a later mcp.detail (last-write-wins)', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'mcp.detail',
          mcpServers: [{ name: 'old-server', status: 'connected' }],
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 2,
          kind: 'mcp.detail',
          mcpServers: [{ name: 'new-server', status: 'pending' }],
        },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: 'MCP servers' }));

    expect(screen.getByText('new-server')).toBeInTheDocument();
    expect(screen.queryByText('old-server')).not.toBeInTheDocument();
  });

  it('renders the read-only Skills page from a skills snapshot', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'skills',
          skills: [
            {
              name: 'pdf-tools',
              description: 'Work with PDF files',
              enabled: true,
              source: 'project',
              path: '.github/skills/pdf-tools',
            },
            { name: 'legacy-skill', enabled: false },
          ],
        },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: 'Skills' }));

    expect(screen.getByText('pdf-tools')).toBeInTheDocument();
    expect(screen.getByText('Work with PDF files')).toBeInTheDocument();
    expect(screen.getByText('project')).toBeInTheDocument();
    expect(screen.getByText('.github/skills/pdf-tools')).toBeInTheDocument();
    expect(screen.getByText('Enabled')).toBeInTheDocument();
    expect(screen.getByText('legacy-skill')).toBeInTheDocument();
    expect(screen.getByText('Disabled')).toBeInTheDocument();
    // Read-only page takeover: no composer is rendered.
    expect(screen.queryByPlaceholderText('Message Copilot…')).not.toBeInTheDocument();
  });

  it('shows the model + context used header from a usage frame', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 5000, tokenLimit: 10000 },
      });
    });

    // Header (above the composer box) shows the active model and 50% context used.
    // Scoped to the header because the model button also shows the active model.
    const header = container.querySelector('.chat-composer__info') as HTMLElement;
    expect(header).not.toBeNull();
    expect(within(header).getByText('gpt-5')).toBeInTheDocument();
    expect(within(header).getByText('50% context used')).toBeInTheDocument();
  });

  it('shows AIC used alongside model and context from a usage frame', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 5000, tokenLimit: 10000, aic: 12.3 },
      });
    });

    const header = container.querySelector('.chat-composer__info') as HTMLElement;
    expect(header).not.toBeNull();
    expect(within(header).getByText('gpt-5')).toBeInTheDocument();
    expect(within(header).getByText('50% context used')).toBeInTheDocument();
    expect(within(header).getByText('12.3 AIC')).toBeInTheDocument();
    expect(header.querySelector('.chat-composer__info-aic')).not.toBeNull();
  });

  it('omits AIC text when a usage frame has no AIC used', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 5000, tokenLimit: 10000 },
      });
    });

    const header = container.querySelector('.chat-composer__info') as HTMLElement;
    expect(header).not.toBeNull();
    expect(within(header).getByText('50% context used')).toBeInTheDocument();
    expect(within(header).queryByText(/AIC/)).not.toBeInTheDocument();
    expect(header.querySelector('.chat-composer__info-aic')).toBeNull();
  });

  it('omits the context % when the usage reading would divide by zero', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 5000, tokenLimit: 0 },
      });
    });

    const header = container.querySelector('.chat-composer__info') as HTMLElement;
    expect(within(header).getByText('gpt-5')).toBeInTheDocument();
    expect(screen.queryByText(/% context/)).not.toBeInTheDocument();
  });

  it('opens the model menu and switches the session model live via More models', async () => {
    window.cs.listModels = vi.fn().mockResolvedValue([
      { id: 'gpt-5', name: 'GPT-5' },
      { id: 'claude-sonnet', name: 'Claude Sonnet' },
    ]);
    render(<ChatViewHost workspace={makeWorkspace()} />);

    // The Model button becomes live (enabled) once the list resolves. waitFor
    // (act-wrapped) settles the async setModels inside act.
    const modelButton = screen.getByRole('button', { name: /Model/ });
    await waitFor(() => expect(modelButton).toBeEnabled());

    fireEvent.click(modelButton);
    expect(screen.getByRole('menu', { name: 'Select model' })).toBeInTheDocument();
    // No usage frame yet => no active model => only the More models section shows.
    fireEvent.click(screen.getByRole('menuitem', { name: /More models/ }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Claude Sonnet' }));

    // A model with no default effort applies with '' effort + the 'default' tier.
    expect(window.cs.setModel).toHaveBeenCalledWith('rich-session', 'claude-sonnet', '', 'default');
    // Selecting closes the menu.
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
  });

  it('threads a chosen reasoning effort through SetModel', async () => {
    window.cs.listModels = vi.fn().mockResolvedValue([
      {
        id: 'sonnet',
        name: 'Sonnet 4.6',
        supportedEfforts: ['low', 'medium', 'high'],
        defaultEffort: 'medium',
      },
    ]);
    render(<ChatViewHost workspace={makeWorkspace()} />);
    // RTL waitFor (act-wrapped) settles the async listModels/setModels inside act;
    // the rich-frame callback is registered synchronously on mount by then.
    await waitFor(() => expect(screen.getByRole('button', { name: /Model/ })).toBeEnabled());
    expect(richFrameCallback).toBeDefined();

    // A usage frame makes 'sonnet' the active model (effort seeds to its default).
    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'sonnet', currentTokens: 1, tokenLimit: 100 },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: /Sonnet 4.6/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Effort/ }));
    fireEvent.click(
      within(screen.getByRole('menu', { name: 'Effort' })).getByRole('menuitemradio', {
        name: 'High',
      }),
    );

    // The raw effort value rides along with the active model + current tier.
    await waitFor(() =>
      expect(window.cs.setModel).toHaveBeenCalledWith('rich-session', 'sonnet', 'high', 'default'),
    );
  });

  it('threads the long-context tier through SetModel', async () => {
    window.cs.listModels = vi.fn().mockResolvedValue([
      { id: 'sonnet', name: 'Sonnet 4.6', supportedEfforts: ['low', 'high'], defaultEffort: 'low' },
    ]);
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: /Model/ })).toBeEnabled());
    expect(richFrameCallback).toBeDefined();

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'sonnet', currentTokens: 1, tokenLimit: 100 },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: /Sonnet 4.6/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Context/ }));
    fireEvent.click(
      within(screen.getByRole('menu', { name: 'Context' })).getByRole('menuitemradio', {
        name: 'Long context',
      }),
    );

    // The model + its seeded default effort ('low') ride along with the new tier.
    await waitFor(() =>
      expect(window.cs.setModel).toHaveBeenCalledWith(
        'rich-session',
        'sonnet',
        'low',
        'long_context',
      ),
    );
  });

  it('leaves the Model button a disabled placeholder when no models are available', async () => {
    window.cs.listModels = vi.fn().mockResolvedValue([]);
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(window.cs.listModels).toHaveBeenCalledWith('rich-session'));

    expect(screen.getByRole('button', { name: /Model/ })).toBeDisabled();
  });

  // --- Resume persistence (rebuilt from the stream, not volatile React state) ---

  it('renders a stream-resolved permission as answered with no active buttons on replay', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // Simulate a fresh-mount replay: the daemon replays both the request AND its
    // resolution. The optimistic local answeredRequests is empty (no click this
    // mount), so answered-state must be reconstructed from the permission.resolved
    // frame alone -- otherwise the card would reappear with live Approve/Reject.
    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'permission.requested',
          requestId: 'p1',
          question: 'Allow shell command?',
          choices: ['approve', 'reject'],
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'permission.resolved', requestId: 'p1', decision: 'approve' },
      });
    });

    // The decision label shows and both buttons are inactive (disabled) -- the
    // component disables rather than removes answered controls (matches a click).
    expect(screen.getByText('approved')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Approve' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reject' })).toBeDisabled();
  });

  it('renders a rejected stream-resolved permission as "rejected" on replay', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'permission.requested',
          requestId: 'p2',
          question: 'Allow shell command?',
          choices: ['approve', 'reject'],
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'permission.resolved', requestId: 'p2', decision: 'reject' },
      });
    });

    expect(screen.getByText('rejected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Approve' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reject' })).toBeDisabled();
  });

  it('renders a stream-resolved user input as answered with no active buttons on replay', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'user_input.requested',
          requestId: 'i1',
          question: 'Pick an option',
          choices: ['Alpha', 'Beta'],
        },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'input.resolved', requestId: 'i1' },
      });
    });

    // The generic "answered" state shows and the choice buttons are disabled, even
    // though no local click happened this mount (reconstructed from input.resolved).
    expect(screen.getByText('answered')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Alpha' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Beta' })).toBeDisabled();
  });

  it('restores the model selector (model + effort + tier) from a model frame after resume', async () => {
    window.cs.listModels = vi.fn().mockResolvedValue([
      {
        id: 'gpt-5',
        name: 'gpt-5',
        supportedEfforts: ['low', 'medium', 'high'],
        defaultEffort: 'medium',
      },
    ]);
    render(<ChatViewHost workspace={makeWorkspace()} />);
    // Wait for the model list to resolve (selector becomes live) before replay.
    await waitFor(() => expect(screen.getByRole('button', { name: /Model/ })).toBeEnabled());
    expect(richFrameCallback).toBeDefined();

    // A resume replays the persisted selection as a 'model' frame; the user made
    // no local pick this mount, so the selector must reflect the stream values.
    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'model',
          model: 'gpt-5',
          effort: 'high',
          contextTier: 'long_context',
        },
      });
    });

    // The selector button shows the restored model + effort (High, NOT the model's
    // default Medium) -- proof the 'model' frame's effort won over the default.
    const modelButton = screen.getByRole('button', { name: /gpt-5/ });
    expect(within(modelButton).getByText('High')).toBeInTheDocument();

    // Opening the menu reflects the restored long-context tier (no local pick).
    fireEvent.click(modelButton);
    const menu = screen.getByRole('menu', { name: 'Select model' });
    expect(within(menu).getByText('Long context')).toBeInTheDocument();
  });

  // --- Typewriter reveal (decoupled from the SDK's bursty delta cadence) --------
  // In jsdom matchMedia is absent, so the reveal snaps to full by default (keeps
  // the other cases simple). These tests install a matchMedia stub to opt the
  // reveal in (matches:false = motion allowed) or to exercise reduced motion
  // (matches:true), and drive the word-by-word reveal with fake timers.
  function installMatchMedia(matches: boolean): void {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
  }

  const LONG_REPLY = 'The quick brown fox jumps over the lazy dog and then keeps on running';

  it('writes a live (delta-built) reply out progressively instead of all at once', async () => {
    installMatchMedia(false); // motion allowed -> the reveal animates
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());
    // Flush the mount's async model fetch before switching to fake timers.
    await act(async () => {});

    vi.useFakeTimers();
    try {
      // A live turn: a single delta burst (the SDK's real cadence) then the full
      // message ~immediately after -- exactly the short-reply case that used to
      // pop in. The reveal must still play out over time.
      act(() => {
        richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: LONG_REPLY } });
        richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.message', text: LONG_REPLY } });
        richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'idle' } });
      });

      const assistant = container.querySelector('.chat-msg--assistant') as HTMLElement;
      // Nothing (or only the first words) revealed yet -- NOT the whole reply.
      expect(assistant.textContent).not.toContain('running');
      // Still writing => the trailing cursor is present.
      expect(container.querySelector('.chat-msg__cursor')).not.toBeNull();

      // Let the reveal play out.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000);
      });
      expect(assistant.textContent).toContain(LONG_REPLY);
      // Done writing => cursor gone.
      expect(container.querySelector('.chat-msg__cursor')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('reveals resumed history (a full message with no deltas) instantly even with motion on', async () => {
    installMatchMedia(false); // motion allowed, yet history must NOT animate
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    // A replayed/historical message arrives as a direct full 'assistant.message'
    // (no preceding deltas) => fromDelta:false => snaps to full with no reveal.
    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.message', text: LONG_REPLY } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'idle' } });
    });

    expect(screen.getByText(LONG_REPLY)).toBeInTheDocument();
  });

  it('snaps a live reply to full under reduced motion', async () => {
    installMatchMedia(true); // prefers-reduced-motion: reduce
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: LONG_REPLY } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.message', text: LONG_REPLY } });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 3, kind: 'idle' } });
    });

    // No timer advance: reduced motion shows the whole reply at once.
    expect(screen.getByText(LONG_REPLY)).toBeInTheDocument();
  });

  it('does not restart an in-flight reveal across a remount (re-subscribe)', async () => {
    installMatchMedia(false);
    const first = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());
    await act(async () => {});

    vi.useFakeTimers();
    try {
      act(() => {
        richFrameCallback?.({ session: 'rich-session', frame: { seq: 1, kind: 'assistant.delta', text: LONG_REPLY } });
        richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'assistant.message', text: LONG_REPLY } });
      });
      // Reveal partway (not finished).
      await act(async () => {
        await vi.advanceTimersByTimeAsync(150);
      });
      const partial = (first.container.querySelector('.chat-msg--assistant') as HTMLElement).textContent ?? '';
      expect(partial.length).toBeGreaterThan(0);
      expect(partial).not.toContain('running');

      // Remount: the per-session frame cache repaints the transcript and the
      // module reveal store keeps progress, so the reveal must NOT restart at 0.
      first.unmount();
      const second = render(<ChatViewHost workspace={makeWorkspace()} />);
      await act(async () => {});
      const afterRemount = (second.container.querySelector('.chat-msg--assistant') as HTMLElement).textContent ?? '';
      expect(afterRemount.length).toBeGreaterThanOrEqual(partial.length);

      // And it still finishes.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000);
      });
      expect((second.container.querySelector('.chat-msg--assistant') as HTMLElement).textContent).toContain(
        LONG_REPLY,
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('opens the find bar on Ctrl+F, counts matches, and closes on Escape', async () => {
    render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'assistant.message', text: 'banana banana' },
      });
      richFrameCallback?.({ session: 'rich-session', frame: { seq: 2, kind: 'idle' } });
    });

    // The find bar is hidden until Ctrl+F.
    expect(screen.queryByLabelText('Find in chat')).toBeNull();

    await act(async () => {
      fireEvent.keyDown(window, { key: 'f', ctrlKey: true });
    });
    const input = screen.getByLabelText('Find in chat');

    // A term that occurs twice in the transcript shows "1/2".
    await act(async () => {
      fireEvent.change(input, { target: { value: 'banana' } });
    });
    expect(screen.getByText('1/2')).toBeInTheDocument();

    // Escape closes the bar.
    await act(async () => {
      fireEvent.keyDown(input, { key: 'Escape' });
    });
    expect(screen.queryByLabelText('Find in chat')).toBeNull();
  });

  // --- Autoscroll + scroll-to-bottom button ------------------------------------
  // jsdom has no real layout, so stub the scroll container's metrics and drive the
  // onScroll / ResizeObserver paths directly.
  function stubScrollMetrics(
    el: HTMLElement,
    { scrollHeight, clientHeight, scrollTop }: { scrollHeight: number; clientHeight: number; scrollTop: number },
  ): { setTop: (v: number) => void; getTop: () => number } {
    let top = scrollTop;
    Object.defineProperty(el, 'scrollHeight', { value: scrollHeight, configurable: true });
    Object.defineProperty(el, 'clientHeight', { value: clientHeight, configurable: true });
    Object.defineProperty(el, 'scrollTop', {
      get: () => top,
      set: (v: number) => {
        top = v;
      },
      configurable: true,
    });
    return { setTop: (v) => (top = v), getTop: () => top };
  }

  it('shows the scroll-to-bottom button only when scrolled up more than 2 viewport heights', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const btn = container.querySelector('.chat-view__scroll-btn') as HTMLElement;
    expect(btn).not.toBeNull();
    expect(btn.className).not.toContain('--visible'); // hidden at the bottom

    const scroll = container.querySelector('.chat-view__scroll') as HTMLElement;
    const handle = stubScrollMetrics(scroll, { scrollHeight: 1000, clientHeight: 200, scrollTop: 0 });

    // Scrolled to the top: distance 800 > 2*200 => the button fades in.
    await act(async () => {
      fireEvent.scroll(scroll);
    });
    expect(btn.className).toContain('--visible');

    // Back near the bottom: distance 40 < 400 => hidden again.
    handle.setTop(760);
    await act(async () => {
      fireEvent.scroll(scroll);
    });
    expect(btn.className).not.toContain('--visible');
  });

  it('smooth-scrolls to the bottom and hides the button when clicked', async () => {
    installMatchMedia(false); // motion allowed => behavior:'smooth'
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    const scroll = container.querySelector('.chat-view__scroll') as HTMLElement;
    stubScrollMetrics(scroll, { scrollHeight: 1000, clientHeight: 200, scrollTop: 0 });
    const scrollTo = vi.fn();
    scroll.scrollTo = scrollTo as unknown as typeof scroll.scrollTo;

    await act(async () => {
      fireEvent.scroll(scroll);
    });
    const btn = container.querySelector('.chat-view__scroll-btn') as HTMLElement;
    expect(btn.className).toContain('--visible');

    await act(async () => {
      fireEvent.click(btn);
    });
    expect(scrollTo).toHaveBeenCalledWith({ top: 1000, behavior: 'smooth' });
    expect(btn.className).not.toContain('--visible');
  });

  it('pins the view to the bottom as content grows while stuck (ResizeObserver)', async () => {
    let roCallback: (() => void) | undefined;
    const RealRO = (window as { ResizeObserver?: unknown }).ResizeObserver;
    (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
      constructor(cb: () => void) {
        roCallback = cb;
      }
      observe(): void {}
      unobserve(): void {}
      disconnect(): void {}
    };
    try {
      const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
      await vi.waitFor(() => expect(richFrameCallback).toBeDefined());
      await act(async () => {});

      const scroll = container.querySelector('.chat-view__scroll') as HTMLElement;
      // Start stuck at the bottom (distance 0 < SCROLL_SLOP).
      const handle = stubScrollMetrics(scroll, { scrollHeight: 1000, clientHeight: 200, scrollTop: 800 });
      await act(async () => {
        fireEvent.scroll(scroll);
      });

      // Content grows; the observer must re-pin scrollTop to the new scrollHeight.
      Object.defineProperty(scroll, 'scrollHeight', { value: 1600, configurable: true });
      expect(roCallback).toBeDefined();
      await act(async () => {
        roCallback?.();
      });
      expect(handle.getTop()).toBe(1600);
    } finally {
      (window as unknown as { ResizeObserver: unknown }).ResizeObserver = RealRO;
    }
  });
});
