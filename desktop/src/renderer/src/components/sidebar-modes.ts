export type SidebarMode = 'manual' | 'group-by-repo' | 'recent-activity' | 'pinned-pending';

export const SIDEBAR_MODES: SidebarMode[] = [
  'manual',
  'group-by-repo',
  'recent-activity',
  'pinned-pending',
];

export const MODE_LABELS: Record<SidebarMode, string> = {
  manual: 'Manual',
  'group-by-repo': 'Group by repo',
  'recent-activity': 'Recent activity',
  'pinned-pending': 'Pinned pending',
};
