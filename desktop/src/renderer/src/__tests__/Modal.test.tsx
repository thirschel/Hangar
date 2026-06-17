// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Modal } from '../components/Modal';

describe('Modal', () => {
  it('renders title and children', () => {
    render(
      <Modal title="Test Modal" onClose={vi.fn()}>
        <p>Modal content</p>
      </Modal>,
    );

    expect(screen.getByText('Test Modal')).toBeInTheDocument();
    expect(screen.getByText('Modal content')).toBeInTheDocument();
  });

  it('renders error message when error prop is provided', () => {
    render(
      <Modal title="Test Modal" onClose={vi.fn()} error="Something went wrong">
        <p>Modal content</p>
      </Modal>,
    );

    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });

  it('renders footer when footer prop is provided', () => {
    render(
      <Modal title="Test Modal" onClose={vi.fn()} footer={<button type="button">Save</button>}>
        <p>Modal content</p>
      </Modal>,
    );

    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument();
  });

  it('has correct ARIA attributes', () => {
    render(
      <Modal title="Test Modal" onClose={vi.fn()}>
        <p>Modal content</p>
      </Modal>,
    );

    expect(screen.getByRole('dialog')).toHaveAttribute('aria-modal', 'true');
  });

  it('matches the snapshot with all props', () => {
    const { container } = render(
      <Modal
        title={<span>Snapshot Modal</span>}
        onClose={vi.fn()}
        footer={<button type="button">Confirm</button>}
        error="Snapshot error"
        className="modal--snapshot"
        busy
      >
        <p>Snapshot content</p>
      </Modal>,
    );

    expect(container).toMatchSnapshot();
  });
});
