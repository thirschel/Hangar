// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { GridPane } from '../components/GridPane';
import type { WorkspaceInfo } from '../../../main/host-client';

// Mock TermView so the grid tests stay focused on layout/logic and avoid pulling
// in xterm/ConPTY internals (covered by TermView's own tests).
vi.mock('../components/TermView', () => ({
  TermView: ({ sessionName }: { sessionName: string | null }) => (
    <div data-testid="termview" data-session={sessionName ?? ''} />
  ),
}));

function mkWs(id: string, over: Partial<WorkspaceInfo> = {}): WorkspaceInfo {
  return {
    id,
    title: `WS ${id}`,
    program: 'copilot',
    repoPath: 'C:/repo',
    worktreePath: 'C:/repo/wt',
    branch: `b-${id}`,
    sessionName: `ws_${id}`,
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
    shell: 'pwsh',
    ...over,
  };
}

describe('GridPane', () => {
  it('renders one TermView tile per workspace, in order', () => {
    render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={() => {}}
      />,
    );
    const tiles = screen.getAllByTestId('termview');
    expect(tiles).toHaveLength(2);
    expect(tiles.map((t) => t.getAttribute('data-session'))).toEqual(['ws_a', 'ws_b']);
  });

  it('applies a manual column count to the grid template', () => {
    const { container } = render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b'), mkWs('c')]}
        columns={2}
        onColumnsChange={() => {}}
        onLeave={() => {}}
      />,
    );
    const grid = container.querySelector('.grid-pane__grid') as HTMLElement;
    expect(grid.style.gridTemplateColumns).toContain('repeat(2,');
  });

  it('changes the column setting via the per-row dropdown', () => {
    const onColumnsChange = vi.fn();
    render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={onColumnsChange}
        onLeave={() => {}}
      />,
    );
    fireEvent.change(screen.getByLabelText('Agents per row'), { target: { value: '2' } });
    expect(onColumnsChange).toHaveBeenCalledWith(2);
  });

  it('calls onLeave when Close grid is clicked', () => {
    const onLeave = vi.fn();
    render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={onLeave}
      />,
    );
    fireEvent.click(screen.getByLabelText('Close grid'));
    expect(onLeave).toHaveBeenCalledTimes(1);
  });

  it('marks the clicked tile as focused (focus ring)', () => {
    const { container } = render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={() => {}}
      />,
    );
    const tiles = container.querySelectorAll('.grid-tile');
    expect(tiles[0].classList.contains('grid-tile--focused')).toBe(false);
    fireEvent.mouseDown(tiles[1]);
    expect(tiles[1].classList.contains('grid-tile--focused')).toBe(true);
    expect(tiles[0].classList.contains('grid-tile--focused')).toBe(false);
  });

  it('renders a drag handle per tile only when onReorder is provided', () => {
    const { container, rerender } = render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={() => {}}
        onReorder={() => {}}
      />,
    );
    expect(container.querySelectorAll('.grid-tile__drag')).toHaveLength(2);

    rerender(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={() => {}}
      />,
    );
    expect(container.querySelectorAll('.grid-tile__drag')).toHaveLength(0);
  });

  it('reorders tiles via drag-and-drop', () => {
    const onReorder = vi.fn();
    const { container } = render(
      <GridPane
        workspaces={[mkWs('a'), mkWs('b'), mkWs('c')]}
        columns={0}
        onColumnsChange={() => {}}
        onLeave={() => {}}
        onReorder={onReorder}
      />,
    );
    const headers = container.querySelectorAll('.grid-tile__header');
    const tiles = container.querySelectorAll('.grid-tile');
    const dataTransfer = { setData: vi.fn(), getData: vi.fn(), effectAllowed: '', dropEffect: '' };

    fireEvent.dragStart(headers[0], { dataTransfer }); // grab tile 'a'
    fireEvent.dragOver(tiles[2], { dataTransfer }); // over tile 'c'
    fireEvent.drop(tiles[2], { dataTransfer }); // drop onto tile 'c'

    // Forward drag lands the dragged tile after the target.
    expect(onReorder).toHaveBeenCalledWith(['b', 'c', 'a']);
  });
});
