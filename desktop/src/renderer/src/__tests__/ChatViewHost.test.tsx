// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
    window.cs.listModels = vi.fn().mockResolvedValue([]);
    window.cs.setModel = vi.fn().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
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

  it('shows the model + context % header from a usage frame', async () => {
    const { container } = render(<ChatViewHost workspace={makeWorkspace()} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'usage', model: 'gpt-5', currentTokens: 5000, tokenLimit: 10000 },
      });
    });

    // Header (above the composer box) shows the active model and 50% context.
    // Scoped to the header because the model button also shows the active model.
    const header = container.querySelector('.chat-composer__info') as HTMLElement;
    expect(header).not.toBeNull();
    expect(within(header).getByText('gpt-5')).toBeInTheDocument();
    expect(within(header).getByText('50% context')).toBeInTheDocument();
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
});
