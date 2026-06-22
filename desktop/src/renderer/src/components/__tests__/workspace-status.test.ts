import { describe, expect, it } from 'vitest';
import type { WorkspaceInfo } from '../../../../main/host-client';
import {
  countByStatus,
  filterByStatus,
  nextStatusFilter,
  workspaceStatus,
  type StatusFilter,
} from '../workspace-status';

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

describe('workspaceStatus', () => {
  it('buckets exited workspaces first', () => {
    expect(workspaceStatus(workspace({ alive: false, waiting: true, busy: true }))).toBe('exited');
  });

  it('buckets alive waiting workspaces', () => {
    expect(workspaceStatus(workspace({ alive: true, waiting: true, busy: true }))).toBe('waiting');
  });

  it('buckets alive busy workspaces that are not waiting', () => {
    expect(workspaceStatus(workspace({ alive: true, waiting: false, busy: true }))).toBe('busy');
  });

  it('buckets alive non-busy workspaces as idle', () => {
    expect(workspaceStatus(workspace({ alive: true, waiting: false, busy: false }))).toBe('idle');
  });
});

describe('countByStatus', () => {
  it('counts all status buckets from the provided full list', () => {
    const list = [
      workspace({ id: 'waiting', waiting: true }),
      workspace({ id: 'busy', busy: true }),
      workspace({ id: 'idle' }),
      workspace({ id: 'exited', alive: false }),
      workspace({ id: 'idle-2' }),
    ];

    expect(countByStatus(list)).toEqual({
      all: 5,
      waiting: 1,
      busy: 1,
      idle: 2,
      exited: 1,
    });
  });
});

describe('status filtering helpers', () => {
  it('filters by one active status without changing all', () => {
    const list = [
      workspace({ id: 'waiting', waiting: true }),
      workspace({ id: 'busy', busy: true }),
      workspace({ id: 'exited', alive: false }),
    ];

    expect(filterByStatus(list, 'all')).toBe(list);
    expect(filterByStatus(list, 'busy').map((w) => w.id)).toEqual(['busy']);
  });

  it('cycles status filters in display order', () => {
    const order: StatusFilter[] = [];
    let current: StatusFilter = 'all';
    for (let i = 0; i < 5; i += 1) {
      current = nextStatusFilter(current);
      order.push(current);
    }

    expect(order).toEqual(['waiting', 'busy', 'idle', 'exited', 'all']);
  });
});
