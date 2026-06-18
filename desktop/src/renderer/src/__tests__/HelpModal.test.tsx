// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { HelpModal } from '../components/HelpModal';

describe('HelpModal', () => {
  it('renders the Keyboard Shortcuts title', () => {
    render(<HelpModal onClose={vi.fn()} />);
    expect(screen.getByText('Keyboard Shortcuts')).toBeInTheDocument();
  });

  it('renders a known shortcut description', () => {
    render(<HelpModal onClose={vi.fn()} />);
    expect(screen.getByText('Push branch')).toBeInTheDocument();
  });

  it('has correct ARIA attributes', () => {
    render(<HelpModal onClose={vi.fn()} />);
    expect(screen.getByRole('dialog')).toHaveAttribute('aria-modal', 'true');
  });

  it('shows a Close button', () => {
    render(<HelpModal onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
  });
});
