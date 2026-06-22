// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
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

  it('does not close when the backdrop (overlay) is clicked', () => {
    const onClose = vi.fn();
    const { container } = render(
      <Modal title="Test Modal" onClose={onClose}>
        <p>Modal content</p>
      </Modal>,
    );

    const overlay = container.querySelector('.modal-overlay') as HTMLElement;
    fireEvent.click(overlay);

    expect(onClose).not.toHaveBeenCalled();
    // Clicking the backdrop must not even begin the exit animation.
    expect(overlay).not.toHaveClass('modal-overlay--closing');
  });

  it('does not close when content inside the modal is clicked', () => {
    const onClose = vi.fn();
    render(
      <Modal title="Test Modal" onClose={onClose}>
        <p>Modal content</p>
      </Modal>,
    );

    fireEvent.click(screen.getByText('Modal content'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('still closes on Escape (begins the exit animation)', () => {
    const onClose = vi.fn();
    const { container } = render(
      <Modal title="Test Modal" onClose={onClose}>
        <p>Modal content</p>
      </Modal>,
    );

    const overlay = container.querySelector('.modal-overlay') as HTMLElement;
    fireEvent.keyDown(document.body, { key: 'Escape' });

    // Esc begins the exit animation (onClose then fires on the overlay's
    // animationend, which a real browser dispatches and jsdom does not).
    expect(overlay).toHaveClass('modal-overlay--closing');
    expect(onClose).not.toHaveBeenCalled();
  });

  it('does not close on Escape while busy', () => {
    const onClose = vi.fn();
    const { container } = render(
      <Modal title="Test Modal" onClose={onClose} busy>
        <p>Modal content</p>
      </Modal>,
    );

    const overlay = container.querySelector('.modal-overlay') as HTMLElement;
    fireEvent.keyDown(document.body, { key: 'Escape' });

    expect(overlay).not.toHaveClass('modal-overlay--closing');
    expect(onClose).not.toHaveBeenCalled();
  });
});
