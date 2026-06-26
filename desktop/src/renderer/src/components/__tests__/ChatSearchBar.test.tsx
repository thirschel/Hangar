// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ChatSearchBar } from '../ChatSearchBar';

function setup(overrides: Partial<Parameters<typeof ChatSearchBar>[0]> = {}) {
  const props = {
    query: 'foo',
    onQueryChange: vi.fn(),
    matchCount: 3,
    activeOrdinal: 1,
    onNext: vi.fn(),
    onPrev: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  render(<ChatSearchBar {...props} />);
  return props;
}

describe('ChatSearchBar', () => {
  it('shows the active/total match count', () => {
    setup({ activeOrdinal: 2, matchCount: 5 });
    expect(screen.getByText('2/5')).toBeInTheDocument();
  });

  it('routes Enter to next and Shift+Enter to previous', () => {
    const props = setup();
    const input = screen.getByLabelText('Find in chat');

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(props.onNext).toHaveBeenCalledTimes(1);
    expect(props.onPrev).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
    expect(props.onPrev).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape and on the close button', () => {
    const props = setup();
    fireEvent.keyDown(screen.getByLabelText('Find in chat'), { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Close search' }));
    expect(props.onClose).toHaveBeenCalledTimes(2);
  });

  it('disables navigation when there are no matches', () => {
    setup({ query: 'zzz', matchCount: 0, activeOrdinal: 0 });
    expect(screen.getByRole('button', { name: 'Next match' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Previous match' })).toBeDisabled();
    expect(screen.getByText('0/0')).toBeInTheDocument();
  });

  it('reports query edits via onQueryChange', () => {
    const props = setup();
    fireEvent.change(screen.getByLabelText('Find in chat'), { target: { value: 'bar' } });
    expect(props.onQueryChange).toHaveBeenCalledWith('bar');
  });
});
