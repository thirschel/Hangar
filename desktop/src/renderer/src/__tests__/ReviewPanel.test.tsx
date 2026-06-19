// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileDiffInfo, WorkspaceInfo } from '../../../main/host-client';
import { MAX_DIFF_PREVIEW_BYTES, ReviewPanel } from '../components/ReviewPanel';

const ws = { id: 'ws1' } as WorkspaceInfo;

const sampleFiles: FileDiffInfo[] = [
  { path: 'a.ts', added: 3, removed: 1 },
  { path: 'b.ts', added: 0, removed: 2 },
];

beforeEach(() => {
  vi.clearAllMocks();
  window.cs.workspaceFiles = vi.fn(async () => sampleFiles);
  window.cs.workspaceFileDiff = vi.fn(async () => '');
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ReviewPanel polling', () => {
  it('fetches once but does not poll while the Changes tab is hidden', async () => {
    vi.useFakeTimers();
    const workspaceFiles = vi.fn(async () => sampleFiles);
    window.cs.workspaceFiles = workspaceFiles;

    await act(async () => {
      render(<ReviewPanel workspace={ws} active={false} />);
    });
    expect(workspaceFiles).toHaveBeenCalledTimes(1);

    // Advance well past several 2.5s poll intervals: still no extra fetches.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });
    expect(workspaceFiles).toHaveBeenCalledTimes(1);
  });

  it('polls every 2.5s while the Changes tab is active', async () => {
    vi.useFakeTimers();
    const workspaceFiles = vi.fn(async () => sampleFiles);
    window.cs.workspaceFiles = workspaceFiles;

    await act(async () => {
      render(<ReviewPanel workspace={ws} active />);
    });
    expect(workspaceFiles).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2500);
    });
    expect(workspaceFiles).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2500);
    });
    expect(workspaceFiles).toHaveBeenCalledTimes(3);
  });

  it('keeps the change-count badge populated even while hidden', async () => {
    const onFilesCount = vi.fn();
    await act(async () => {
      render(<ReviewPanel workspace={ws} active={false} onFilesCount={onFilesCount} />);
    });
    await waitFor(() => expect(onFilesCount).toHaveBeenLastCalledWith(sampleFiles.length));
  });
});

describe('ReviewPanel large-diff guard', () => {
  it('shows a "too large" notice instead of rendering an oversized diff', async () => {
    window.cs.workspaceFiles = vi.fn(async () => [{ path: 'big.ts', added: 1, removed: 0 }]);
    const huge = 'diff --git a/big.ts b/big.ts\n' + 'x'.repeat(MAX_DIFF_PREVIEW_BYTES);
    window.cs.workspaceFileDiff = vi.fn(async () => huge);

    await act(async () => {
      render(<ReviewPanel workspace={ws} />);
    });
    const row = await screen.findByText('big.ts');
    await act(async () => {
      fireEvent.click(row);
    });

    expect(await screen.findByText(/Diff too large to preview/)).toBeInTheDocument();
  });

  it('renders a normal-sized diff inline', async () => {
    window.cs.workspaceFiles = vi.fn(async () => [{ path: 'a.ts', added: 1, removed: 1 }]);
    const smallDiff = [
      'diff --git a/a.ts b/a.ts',
      'index 1111111..2222222 100644',
      '--- a/a.ts',
      '+++ b/a.ts',
      '@@ -1,2 +1,2 @@',
      '-old line',
      '+new line',
      ' context',
      '',
    ].join('\n');
    window.cs.workspaceFileDiff = vi.fn(async () => smallDiff);

    const { container } = render(<ReviewPanel workspace={ws} />);
    const row = await screen.findByText('a.ts');
    await act(async () => {
      fireEvent.click(row);
    });

    await waitFor(() => expect(container.querySelector('.diff-file__header')).toBeTruthy());
    expect(screen.queryByText(/Diff too large to preview/)).toBeNull();
  });
});