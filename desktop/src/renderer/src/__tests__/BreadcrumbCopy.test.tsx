// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BreadcrumbCopy } from '../components/BreadcrumbCopy';

const writeText = vi.fn(() => Promise.resolve());

beforeEach(() => {
  writeText.mockClear();
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
    writable: true,
  });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('BreadcrumbCopy', () => {
  const repoPath = 'C:\\dev\\my-repo';

  it('renders the label and hides the tooltip initially', () => {
    render(<BreadcrumbCopy label="my-repo" path={repoPath} tipAriaLabel="Copy repo path" />);

    const btn = screen.getByRole('button', { name: 'Copy repo path' });
    expect(btn).toHaveTextContent('my-repo');
    // The tooltip is aria-hidden while not shown, so it is not in the a11y tree.
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  it('shows the path in a tooltip on hover', () => {
    render(<BreadcrumbCopy label="my-repo" path={repoPath} tipAriaLabel="Copy repo path" />);

    const btn = screen.getByRole('button', { name: 'Copy repo path' });
    fireEvent.mouseEnter(btn);

    const tip = screen.getByRole('tooltip');
    expect(tip).toHaveTextContent(repoPath);
    expect(tip).toHaveClass('is-visible');
  });

  it('copies the path and shows "Copied to clipboard" on click', () => {
    render(<BreadcrumbCopy label="my-repo" path={repoPath} tipAriaLabel="Copy repo path" />);

    const btn = screen.getByRole('button', { name: 'Copy repo path' });
    fireEvent.mouseEnter(btn);
    fireEvent.click(btn);

    expect(writeText).toHaveBeenCalledWith(repoPath);
    expect(screen.getByRole('tooltip')).toHaveTextContent('Copied to clipboard');
  });

  it('copies on Enter/Space keypress', () => {
    const worktreePath = 'C:\\dev\\wt\\main';
    render(<BreadcrumbCopy label="main" path={worktreePath} tipAriaLabel="Copy workspace path" />);

    const btn = screen.getByRole('button', { name: 'Copy workspace path' });
    fireEvent.keyDown(btn, { key: 'Enter' });

    expect(writeText).toHaveBeenCalledWith(worktreePath);
    expect(screen.getByRole('tooltip')).toHaveTextContent('Copied to clipboard');
  });

  it('fades the tooltip away after copying', () => {
    vi.useFakeTimers();
    render(<BreadcrumbCopy label="my-repo" path={repoPath} tipAriaLabel="Copy repo path" />);

    const btn = screen.getByRole('button', { name: 'Copy repo path' });
    fireEvent.mouseEnter(btn);
    fireEvent.click(btn);
    expect(screen.getByRole('tooltip')).toHaveTextContent('Copied to clipboard');

    act(() => {
      vi.advanceTimersByTime(1000);
    });

    // After the dwell the tooltip is hidden (aria-hidden), out of the a11y tree.
    expect(screen.queryByRole('tooltip')).toBeNull();
  });
});
