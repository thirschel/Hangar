import type { WorkspaceInfo } from '../../../main/host-client';

export type WorkspaceStatus = 'waiting' | 'busy' | 'idle' | 'exited';
export type StatusFilter = 'all' | WorkspaceStatus;
export type StatusCounts = Record<WorkspaceStatus, number> & { all: number };

export const STATUS_FILTERS: readonly StatusFilter[] = [
  'all',
  'waiting',
  'busy',
  'idle',
  'exited',
] as const;

export function isStatusFilter(value: string | null): value is StatusFilter {
  return STATUS_FILTERS.includes(value as StatusFilter);
}

export function nextStatusFilter(current: StatusFilter): StatusFilter {
  const idx = STATUS_FILTERS.indexOf(current);
  return STATUS_FILTERS[(idx + 1) % STATUS_FILTERS.length];
}

export function workspaceStatus(w: WorkspaceInfo): WorkspaceStatus {
  if (!w.alive) return 'exited';
  if (w.waiting) return 'waiting';
  if (w.busy) return 'busy';
  return 'idle';
}

export function countByStatus(list: WorkspaceInfo[]): StatusCounts {
  const counts: StatusCounts = {
    all: list.length,
    waiting: 0,
    busy: 0,
    idle: 0,
    exited: 0,
  };
  for (const w of list) {
    counts[workspaceStatus(w)] += 1;
  }
  return counts;
}

export function filterByStatus(list: WorkspaceInfo[], filter: StatusFilter): WorkspaceInfo[] {
  if (filter === 'all') return list;
  return list.filter((w) => workspaceStatus(w) === filter);
}
