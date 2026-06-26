// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { McpCatalog } from '../../../../preload';
import type { McpServerInfo, WorkspaceInfo } from '../../../../main/host-client';
import { McpPage } from '../McpPage';

function makeWorkspace(overrides: Partial<WorkspaceInfo> = {}): WorkspaceInfo {
  return {
    id: 'ws-1',
    kind: 'rich',
    title: 'Repo chat',
    program: 'copilot',
    repoPath: 'C:\\repo',
    repoKey: 'repo-key',
    worktreePath: 'C:\\repo',
    branch: 'main',
    sessionName: 'session-1',
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

const catalog: McpCatalog = {
  servers: {
    filesystem: {
      type: 'local',
      command: 'agency.exe',
      args: ['--stdio'],
      env: { DEBUG: '1' },
      cwd: 'C:\\repo',
      tools: ['read_file'],
      timeout: 30,
    },
    github: {
      type: 'http',
      url: 'https://mcp.example.test',
      headers: { Authorization: 'Bearer token' },
      tools: ['*'],
      timeout: 10,
    },
  },
  repoEnabled: { 'repo-key': ['filesystem'] },
};

const liveServers: McpServerInfo[] = [
  {
    name: 'filesystem',
    status: 'connected',
    transport: 'stdio',
    source: 'user',
    tools: ['read_file'],
  },
];

describe('McpPage', () => {
  let changed: ((catalog: McpCatalog) => void) | undefined;
  let unsubscribe: () => void;
  let unsubscribeCalled: boolean;

  beforeEach(() => {
    changed = undefined;
    unsubscribeCalled = false;
    unsubscribe = () => {
      unsubscribeCalled = true;
    };
    window.cs.mcpRead = vi.fn().mockResolvedValue(catalog);
    window.cs.mcpUpsertServer = vi.fn().mockResolvedValue(catalog);
    window.cs.mcpRemoveServer = vi.fn().mockResolvedValue(catalog);
    window.cs.mcpSetEnabled = vi.fn().mockResolvedValue(catalog);
    window.cs.regenerateAgent = vi.fn().mockResolvedValue(undefined);
    window.cs.onMcpChanged = vi.fn((callback: (next: McpCatalog) => void) => {
      changed = callback;
      return unsubscribe;
    });
  });

  it('renders live status and catalog rows', async () => {
    render(<McpPage servers={liveServers} workspace={makeWorkspace()} />);

    expect(screen.getByRole('heading', { name: 'Live status' })).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('stdio')).toBeInTheDocument();

    const row = await screen.findByText('github');
    expect(row).toBeInTheDocument();
    expect(screen.getByText('https://mcp.example.test')).toBeInTheDocument();
  });

  it('toggles per-repo enablement using workspace.repoKey', async () => {
    render(<McpPage servers={[]} workspace={makeWorkspace()} />);

    const githubRow = (await screen.findByText('github')).closest('.mcp-page__catalog-row');
    expect(githubRow).not.toBeNull();
    const toggle = within(githubRow as HTMLElement).getByRole('checkbox');

    fireEvent.click(toggle);

    await waitFor(() => {
      expect(window.cs.mcpSetEnabled).toHaveBeenCalledWith('repo-key', 'github', true);
    });
  });

  it('adds, edits, and deletes global server definitions through the catalog APIs', async () => {
    render(<McpPage servers={[]} workspace={makeWorkspace()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Add server' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'local-new' } });
    fireEvent.change(screen.getByLabelText(/Command/), { target: { value: 'new-mcp.exe' } });
    fireEvent.change(screen.getByLabelText('Args'), { target: { value: '--stdio\n--verbose' } });
    fireEvent.change(screen.getByLabelText('Env'), { target: { value: 'TOKEN=abc' } });
    fireEvent.change(screen.getByLabelText('Tools'), { target: { value: 'tool_a\ntool_b' } });
    fireEvent.change(screen.getByLabelText('Timeout seconds'), { target: { value: '42' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save server' }));

    await waitFor(() => {
      expect(window.cs.mcpUpsertServer).toHaveBeenCalledWith('local-new', {
        type: 'local',
        command: 'new-mcp.exe',
        args: ['--stdio', '--verbose'],
        env: { TOKEN: 'abc' },
        tools: ['tool_a', 'tool_b'],
        timeout: 42,
      });
    });

    const githubRow = (await screen.findByText('github')).closest('.mcp-page__catalog-row');
    expect(githubRow).not.toBeNull();
    fireEvent.click(within(githubRow as HTMLElement).getByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByLabelText('URL'), {
      target: { value: 'https://new.example.test' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save server' }));

    await waitFor(() => {
      expect(window.cs.mcpUpsertServer).toHaveBeenLastCalledWith(
        'github',
        expect.objectContaining({ type: 'http', url: 'https://new.example.test' }),
      );
    });

    fireEvent.click(within(githubRow as HTMLElement).getByRole('button', { name: 'Delete' }));

    // Deleting now requires confirming in the modal — the API is not called yet.
    expect(window.cs.mcpRemoveServer).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole('button', { name: 'Delete server' }));

    await waitFor(() => {
      expect(window.cs.mcpRemoveServer).toHaveBeenCalledWith('github');
    });
  });

  it('cancels deletion without calling the remove API', async () => {
    render(<McpPage servers={[]} workspace={makeWorkspace()} />);

    const githubRow = (await screen.findByText('github')).closest('.mcp-page__catalog-row');
    fireEvent.click(within(githubRow as HTMLElement).getByRole('button', { name: 'Delete' }));

    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));

    expect(window.cs.mcpRemoveServer).not.toHaveBeenCalled();
  });

  it('hides per-repo toggles in global-only mode', async () => {
    render(<McpPage servers={[]} workspace={makeWorkspace({ repoKey: undefined })} />);

    await screen.findByText('github');

    expect(
      screen.getByText('Open a repo-backed chat to enable servers per repository.'),
    ).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });

  it('refreshes from onMcpChanged and unsubscribes on unmount', async () => {
    const { unmount } = render(<McpPage servers={[]} workspace={makeWorkspace()} />);
    await screen.findByText('github');

    act(() => {
      changed?.({
        servers: {
          slack: { type: 'http', url: 'https://slack.example.test', tools: ['*'], timeout: 5 },
        },
        repoEnabled: { 'repo-key': ['slack'] },
      });
    });

    expect(await screen.findByText('slack')).toBeInTheDocument();
    expect(screen.queryByText('github')).not.toBeInTheDocument();

    unmount();
    expect(unsubscribeCalled).toBe(true);
  });
});
