import { useCallback, useEffect, useRef, useState } from 'react';
import type { WorkspaceInfo } from '../../../main/host-client';
import type { ShellProfile } from '../../../main/settings';
import { ShellTerminal } from './ShellTerminal';
import type { TermViewHandle } from './TermView';
import '../terminal.css';

type CenterTerminalProps = {
  workspace: WorkspaceInfo | null;
};

const HEIGHT_KEY = 'cs.terminalHeight';
const DEFAULT_HEIGHT = 280;
const MIN_HEIGHT = 120;
// Space kept above the dock (agent header + a usable agent terminal) when dragging.
const TOP_RESERVE = 180;

// CenterTerminal is the on-demand shell dock at the bottom of the agent (center)
// view. It keeps the original collapsible behavior — collapsed to a bar by default,
// nothing spawned until opened, the instance kept alive across collapse/expand, and
// only ✕ kills it — and adds a draggable top edge to resize its height (persisted).
// Living in the center pane gives the shell the full center-column width.
export function CenterTerminal({ workspace }: CenterTerminalProps): JSX.Element {
  const [bottomOpen, setBottomOpen] = useState(false);
  const [terminalCreated, setTerminalCreated] = useState(false);
  const [height, setHeight] = useState<number>(() => {
    const saved = Number(localStorage.getItem(HEIGHT_KEY));
    return Number.isFinite(saved) && saved >= MIN_HEIGHT ? saved : DEFAULT_HEIGHT;
  });
  const [dragging, setDragging] = useState(false);
  const [profiles, setProfiles] = useState<ShellProfile[]>([]);
  const [activeProfileId, setActiveProfileId] = useState('');

  const shellRef = useRef<TermViewHandle>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const mountedRef = useRef(true);
  // Mirror height into a ref so the (stable) drag handler reads the latest value.
  const heightRef = useRef(height);
  useEffect(() => {
    heightRef.current = height;
  }, [height]);

  const wsId = workspace?.id ?? null;

  const loadProfiles = useCallback(async (): Promise<void> => {
    try {
      const settings = await window.cs.getSettings();
      if (!mountedRef.current) return;
      const nextProfiles = settings.terminalProfiles ?? [];
      setProfiles(nextProfiles);
      setActiveProfileId((current) => {
        if (current && nextProfiles.some((profile) => profile.id === current)) return current;
        const configured = settings.defaultTerminalProfileId;
        return nextProfiles.find((profile) => profile.id === configured)?.id ?? nextProfiles[0]?.id ?? '';
      });
    } catch {
      // Keep the terminal usable with the backend default if settings are unavailable.
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    void loadProfiles();
    return () => {
      mountedRef.current = false;
    };
  }, [loadProfiles]);

  // Don't carry a terminal across workspaces; the daemon keeps the shell alive for
  // re-open. Collapse back to the bar when the selection changes.
  useEffect(() => {
    setBottomOpen(false);
    setTerminalCreated(false);
  }, [wsId]);

  const refitSoon = (): void => {
    setTimeout(() => shellRef.current?.refit(), 0);
  };

  // Open (creating on first use) the terminal — slides the dock up.
  const openTerminal = (): void => {
    void loadProfiles();
    setTerminalCreated(true);
    setBottomOpen(true);
    refitSoon();
  };

  // The arrow only toggles visibility; the instance is kept alive while collapsed.
  const toggleBottom = (): void => {
    if (bottomOpen) setBottomOpen(false);
    else openTerminal();
  };

  // ✕ closes the instance for good and collapses back to the bar.
  const killTerminal = (): void => {
    if (wsId) void window.cs.closeShell(wsId);
    setTerminalCreated(false);
    setBottomOpen(false);
  };

  // Drag the top edge to resize the dock height. Drag up grows it; clamped between
  // MIN_HEIGHT and the center-pane height minus a reserve for the agent above.
  const onResizeStart = useCallback((e: React.MouseEvent): void => {
    e.preventDefault();
    const startY = e.clientY;
    const startH = heightRef.current;
    const parent = rootRef.current?.parentElement ?? null;
    let last = startH;
    setDragging(true);
    document.body.classList.add('is-row-resizing');
    const onMove = (ev: MouseEvent): void => {
      const maxH = Math.max(MIN_HEIGHT, (parent?.clientHeight ?? 800) - TOP_RESERVE);
      last = Math.min(maxH, Math.max(MIN_HEIGHT, startH + (startY - ev.clientY)));
      setHeight(last);
    };
    const onUp = (): void => {
      setDragging(false);
      document.body.classList.remove('is-row-resizing');
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      localStorage.setItem(HEIGHT_KEY, String(Math.round(last)));
      shellRef.current?.refit();
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, []);

  const activeProfile = profiles.find((profile) => profile.id === activeProfileId) ?? profiles[0];
  const program = activeProfile
    ? [activeProfile.command, ...(activeProfile.args ?? [])].join(' ')
    : undefined;
  const selectedProfileId = activeProfile?.id ?? '';

  return (
    <div
      ref={rootRef}
      className={`center-terminal${bottomOpen ? ' center-terminal--open' : ''}${
        dragging ? ' center-terminal--dragging' : ''
      }`}
      style={bottomOpen ? { height } : undefined}
    >
      {bottomOpen && (
        <div
          className="center-terminal__resizer"
          role="separator"
          aria-orientation="horizontal"
          aria-label="Resize terminal"
          title="Drag to resize"
          onMouseDown={onResizeStart}
        />
      )}
      <div className="mini-tabs" aria-label="Terminal">
        <button
          className="center-terminal__collapse"
          type="button"
          title={bottomOpen ? 'Collapse terminal' : 'Open terminal'}
          aria-expanded={bottomOpen}
          onClick={toggleBottom}
          disabled={!workspace}
        >
          {bottomOpen ? '▾' : '▸'}
        </button>
        <button
          className={`mini-tab${bottomOpen ? ' mini-tab--active' : ''}`}
          type="button"
          onClick={openTerminal}
          disabled={!workspace}
        >
          Terminal
        </button>
        <div className="tab-bar__spacer" />
        {profiles.length > 0 && (
          <select
            className="center-terminal__shell-select"
            aria-label="Shell"
            value={selectedProfileId}
            onChange={(event) => setActiveProfileId(event.target.value)}
            disabled={!workspace}
          >
            {profiles.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.label}
              </option>
            ))}
          </select>
        )}
        {terminalCreated && (
          <button
            className="icon-button center-terminal__kill"
            type="button"
            title="Close terminal"
            onClick={killTerminal}
          >
            ✕
          </button>
        )}
      </div>
      {terminalCreated && (
        <div className="center-terminal__body">
          <ShellTerminal
            ref={shellRef}
            key={`${wsId ?? 'none'}:${activeProfileId}`}
            workspace={workspace}
            program={program}
          />
        </div>
      )}
    </div>
  );
}
