// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkspaceInfo } from '../../../../main/host-client';
import { Sidebar } from '../Sidebar';
import { countByStatus } from '../workspace-status';

type SidebarProps = Parameters<typeof Sidebar>[0];

function workspace(overrides: Partial<WorkspaceInfo>): WorkspaceInfo {
  return {
    id: 'ws',
    title: 'Workspace',
    program: 'copilot',
    repoPath: 'C:\\repo',
    worktreePath: 'C:\\repo\\.hangar',
    branch: 'feature',
    sessionName: 'ws_session',
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

function baseProps(overrides: Partial<SidebarProps> = {}): SidebarProps {
  const workspaces = overrides.workspaces ?? [];
  return {
    workspaces,
    selectedId: null,
    onSelect: vi.fn(),
    onArchive: vi.fn(),
    onSettings: vi.fn(),
    onNewWorkspace: vi.fn(),
    onNewAtRepo: vi.fn(),
    onCycleMode: vi.fn(),
    sidebarMode: 'manual',
    filter: '',
    onFilterChange: vi.fn(),
    statusFilter: 'all',
    counts: countByStatus(workspaces),
    onStatusFilterChange: vi.fn(),
    ...overrides,
  };
}

describe('Sidebar grid multi-select', () => {
  it('renders a grid checkbox for alive workspaces but not exited ones', () => {
    const workspaces = [
      workspace({ id: 'a', title: 'Alpha', alive: true }),
      workspace({ id: 'b', title: 'Bravo', alive: false }),
    ];

    render(<Sidebar {...baseProps({ workspaces, onToggleGridMember: vi.fn() })} />);

    expect(screen.getByRole('checkbox', { name: 'Add Alpha to grid' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Add Bravo to grid' })).toBeNull();
  });

  it('toggles grid membership on checkbox click without selecting the row', () => {
    const onToggleGridMember = vi.fn();
    const onSelect = vi.fn();
    const workspaces = [workspace({ id: 'a', title: 'Alpha' })];

    render(<Sidebar {...baseProps({ workspaces, onToggleGridMember, onSelect })} />);

    fireEvent.click(screen.getByRole('checkbox', { name: 'Add Alpha to grid' }));

    expect(onToggleGridMember).toHaveBeenCalledTimes(1);
    expect(onToggleGridMember).toHaveBeenCalledWith('a');
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('checks selected rows and shows a header summary with a working Clear button', () => {
    const onClearGridSelection = vi.fn();
    const workspaces = [
      workspace({ id: 'a', title: 'Alpha' }),
      workspace({ id: 'b', title: 'Bravo' }),
    ];

    render(
      <Sidebar
        {...baseProps({
          workspaces,
          onToggleGridMember: vi.fn(),
          onClearGridSelection,
          gridSelectedIds: new Set(['a']),
        })}
      />,
    );

    expect(screen.getByRole('checkbox', { name: 'Add Alpha to grid' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Add Bravo to grid' })).not.toBeChecked();
    expect(screen.getByText('1 selected')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(onClearGridSelection).toHaveBeenCalledTimes(1);
  });

  it('threads grid selection through grouped-by-repo mode', () => {
    const workspaces = [
      workspace({ id: 'a', title: 'Alpha', repoPath: 'C:\\repo-one' }),
      workspace({ id: 'b', title: 'Bravo', repoPath: 'C:\\repo-two' }),
    ];

    render(
      <Sidebar
        {...baseProps({ workspaces, sidebarMode: 'group-by-repo', onToggleGridMember: vi.fn() })}
      />,
    );

    expect(screen.getByRole('checkbox', { name: 'Add Alpha to grid' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Add Bravo to grid' })).toBeInTheDocument();
  });

  it('renders no checkboxes or summary when grid selection is disabled', () => {
    const workspaces = [workspace({ id: 'a', title: 'Alpha' })];

    render(<Sidebar {...baseProps({ workspaces })} />);

    expect(screen.queryAllByRole('checkbox')).toHaveLength(0);
    expect(screen.queryByRole('button', { name: 'Clear' })).toBeNull();
  });
});

describe('Sidebar worktree icon', () => {
  it('shows the worktree icon only for worktree-backed sessions', () => {
    const workspaces = [
      workspace({ id: 'a', title: 'Alpha', hasWorktree: true }),
      workspace({ id: 'b', title: 'Bravo', hasWorktree: false }),
    ];

    render(<Sidebar {...baseProps({ workspaces })} />);

    expect(screen.getAllByLabelText('Isolated git worktree')).toHaveLength(1);
  });
});

describe('Sidebar labels', () => {
  it('uses the standard wording by default', () => {
    render(<Sidebar {...baseProps()} />);

    expect(screen.getByText('Workspaces')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Filter workspaces… (/)')).toBeInTheDocument();
    expect(screen.getByText('No workspaces yet')).toBeInTheDocument();
  });

  it('reads naturally for chats when title/noun/emptyHint are provided', () => {
    render(
      <Sidebar
        {...baseProps({
          title: 'Chats',
          noun: 'chat',
          emptyHint: <p>Start a Copilot chat.</p>,
        })}
      />,
    );

    expect(screen.getByText('Chats')).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Chats' })).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Filter chats… (/)')).toBeInTheDocument();
    expect(screen.getByTitle('New chat (n)')).toBeInTheDocument();
    expect(screen.getByText('No chats yet')).toBeInTheDocument();
    expect(screen.getByText('Start a Copilot chat.')).toBeInTheDocument();
  });
});