/**
 * Single source of truth for the keyboard shortcuts shown in the Help modal.
 * Keep this in sync with the desktop keydown handler in App.tsx (NOT the Go TUI keymap).
 */
export type ShortcutItem = {
  /** Display string for the key(s), e.g. "j / ↓". */
  keys: string;
  /** What the shortcut does. */
  description: string;
};

export type ShortcutGroup = {
  heading: string;
  items: ShortcutItem[];
};

export const SHORTCUT_GROUPS: ShortcutGroup[] = [
  {
    heading: 'Navigation',
    items: [
      { keys: 'j / ↓', description: 'Select next workspace' },
      { keys: 'k / ↑', description: 'Select previous workspace' },
      { keys: 'Alt+1–9', description: 'Jump to workspace by index' },
    ],
  },
  {
    heading: 'Workspace Actions',
    items: [
      { keys: 'n / Ctrl+N', description: 'New workspace' },
      { keys: 'p', description: 'Push branch' },
      { keys: 'D', description: 'Kill / archive workspace' },
      { keys: 'b', description: 'Browse Copilot sessions' },
    ],
  },
  {
    heading: 'Sidebar',
    items: [
      { keys: 's / S', description: 'Cycle sidebar mode ↔' },
      { keys: '/', description: 'Search / filter workspaces' },
      { keys: 'J / K', description: 'Reorder (Manual mode only)' },
    ],
  },
  {
    heading: 'General',
    items: [
      { keys: '?', description: 'Toggle this help' },
      { keys: 'Ctrl+,', description: 'Settings' },
      { keys: 'q', description: 'Quit' },
      { keys: 'Esc', description: 'Close search / help' },
    ],
  },
];
