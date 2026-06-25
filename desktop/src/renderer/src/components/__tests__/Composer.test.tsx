// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Composer, type ComposerProps } from '../Composer';

function baseProps(overrides: Partial<ComposerProps> = {}): ComposerProps {
  return {
    turnInProgress: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    ...overrides,
  };
}

describe('Composer', () => {
  it('sends trimmed text on click and clears the box', () => {
    const onSend = vi.fn();
    render(<Composer {...baseProps({ onSend })} />);

    const textbox = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(textbox, { target: { value: '  hello world  ' } });

    const send = screen.getByRole('button', { name: 'Send' });
    expect(send).toBeEnabled();
    fireEvent.click(send);

    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith('hello world');
    expect(textbox.value).toBe('');
  });

  it('disables Send when the draft is empty or only whitespace', () => {
    render(<Composer {...baseProps()} />);
    const send = screen.getByRole('button', { name: 'Send' });
    expect(send).toBeDisabled();

    fireEvent.change(screen.getByRole('textbox'), { target: { value: '   ' } });
    expect(send).toBeDisabled();
  });

  it('disables Send while a turn is in progress and ignores clicks', () => {
    const onSend = vi.fn();
    render(<Composer {...baseProps({ turnInProgress: true, onSend })} />);

    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'hi' } });
    const send = screen.getByRole('button', { name: 'Send' });
    expect(send).toBeDisabled();
    fireEvent.click(send);
    expect(onSend).not.toHaveBeenCalled();
  });

  it('hard-disables Send via disabledSend even with text', () => {
    render(<Composer {...baseProps({ disabledSend: true })} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'hi' } });
    expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
  });

  it('submits on Ctrl+Enter and clears the box', () => {
    const onSend = vi.fn();
    render(<Composer {...baseProps({ onSend })} />);

    const textbox = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(textbox, { target: { value: 'via shortcut' } });
    fireEvent.keyDown(textbox, { key: 'Enter', ctrlKey: true });

    expect(onSend).toHaveBeenCalledWith('via shortcut');
    expect(textbox.value).toBe('');
  });

  it('does not submit on Enter without a modifier', () => {
    const onSend = vi.fn();
    render(<Composer {...baseProps({ onSend })} />);

    const textbox = screen.getByRole('textbox');
    fireEvent.change(textbox, { target: { value: 'no send' } });
    fireEvent.keyDown(textbox, { key: 'Enter' });

    expect(onSend).not.toHaveBeenCalled();
  });

  it('shows Stop only while a turn is in progress and calls onStop', () => {
    const onStop = vi.fn();
    const { rerender } = render(<Composer {...baseProps({ turnInProgress: false, onStop })} />);
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();

    rerender(<Composer {...baseProps({ turnInProgress: true, onStop })} />);
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it('renders the upload and model selector as disabled placeholders', () => {
    render(<Composer {...baseProps()} />);
    expect(screen.getByTitle(/attachments/i)).toBeDisabled();
    expect(screen.getByTitle(/model selector/i)).toBeDisabled();
  });

  it('renders the info slot above the box only when provided', () => {
    const { container, rerender } = render(
      <Composer {...baseProps({ info: <span>gpt-5 - 18%</span> })} />,
    );
    expect(screen.getByText('gpt-5 - 18%')).toBeInTheDocument();
    expect(container.querySelector('.chat-composer__info')).not.toBeNull();

    rerender(<Composer {...baseProps()} />);
    expect(container.querySelector('.chat-composer__info')).toBeNull();
  });
});
