// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    expect(onSend).toHaveBeenCalledWith('hello world', []);
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

    expect(onSend).toHaveBeenCalledWith('via shortcut', []);
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

  it('keeps the Model button a placeholder when no models, with Upload live', () => {
    render(<Composer {...baseProps()} />);
    // Upload is always live now (file attachments), even with no models.
    expect(screen.getByRole('button', { name: 'Attach files' })).toBeEnabled();
    // With no models / no handler the Model selector stays a disabled placeholder.
    const model = screen.getByRole('button', { name: /Model/ });
    expect(model).toBeDisabled();
    expect(model).toHaveAttribute('title', 'No models available');
  });

  it('opens the model menu, marks the active model and reports a selection', () => {
    const onSelectModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [
            { id: 'gpt-5', name: 'GPT-5' },
            { id: 'claude', name: 'Claude' },
          ],
          currentModelId: 'gpt-5',
          onSelectModel,
        })}
      />,
    );

    // The button label reflects the current model; the menu is closed initially.
    const modelButton = screen.getByRole('button', { name: /GPT-5/ });
    expect(modelButton).toBeEnabled();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();

    fireEvent.click(modelButton);
    expect(screen.getByRole('menu', { name: 'Select model' })).toBeInTheDocument();
    expect(screen.getByRole('menuitemradio', { name: 'GPT-5' })).toHaveAttribute(
      'aria-checked',
      'true',
    );

    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Claude' }));
    expect(onSelectModel).toHaveBeenCalledWith('claude');
    // Selecting closes the menu.
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
  });

  it('falls back to the model id as the menu label when no name is provided', () => {
    render(
      <Composer {...baseProps({ models: [{ id: 'bare-id' }], onSelectModel: vi.fn() })} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Model/ }));
    expect(screen.getByRole('menuitemradio', { name: 'bare-id' })).toBeInTheDocument();
  });

  it('closes the model menu on Escape without selecting', () => {
    const onSelectModel = vi.fn();
    render(
      <Composer {...baseProps({ models: [{ id: 'gpt-5', name: 'GPT-5' }], onSelectModel })} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Model/ }));
    expect(screen.getByRole('menu', { name: 'Select model' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
    expect(onSelectModel).not.toHaveBeenCalled();
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

describe('Composer attachments', () => {
  it('adds picked files as removable chips (basenames) and de-duplicates by path', async () => {
    window.cs.pickFiles = vi.fn().mockResolvedValue(['/a/x.go', '/b/y.ts']);
    render(<Composer {...baseProps()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));

    // Chips render the basenames of the chosen absolute paths.
    expect(await screen.findByText('x.go')).toBeInTheDocument();
    expect(screen.getByText('y.ts')).toBeInTheDocument();

    // Picking the same paths again does not duplicate the existing chips.
    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));
    await waitFor(() => expect(window.cs.pickFiles).toHaveBeenCalledTimes(2));
    expect(screen.getAllByText('x.go')).toHaveLength(1);
    expect(screen.getAllByText('y.ts')).toHaveLength(1);
  });

  it('drops a chip when its remove button is clicked', async () => {
    window.cs.pickFiles = vi.fn().mockResolvedValue(['/a/x.go', '/b/y.ts']);
    render(<Composer {...baseProps()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));
    expect(await screen.findByText('x.go')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Remove x.go' }));

    expect(screen.queryByText('x.go')).not.toBeInTheDocument();
    expect(screen.getByText('y.ts')).toBeInTheDocument();
  });

  it('sends text + attachments via onSend and clears both', async () => {
    const onSend = vi.fn();
    window.cs.pickFiles = vi.fn().mockResolvedValue(['/a/x.go', '/b/y.ts']);
    render(<Composer {...baseProps({ onSend })} />);

    const textbox = screen.getByRole('textbox') as HTMLTextAreaElement;
    fireEvent.change(textbox, { target: { value: 'with files' } });
    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));
    expect(await screen.findByText('x.go')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Send' }));

    expect(onSend).toHaveBeenCalledWith('with files', ['/a/x.go', '/b/y.ts']);
    // Both the draft and the chips clear after a successful send.
    expect(textbox.value).toBe('');
    expect(screen.queryByText('x.go')).not.toBeInTheDocument();
    expect(screen.queryByText('y.ts')).not.toBeInTheDocument();
  });

  it('enables Send with at least one attachment even when the text is empty', async () => {
    const onSend = vi.fn();
    window.cs.pickFiles = vi.fn().mockResolvedValue(['/a/x.go']);
    render(<Composer {...baseProps({ onSend })} />);

    const send = screen.getByRole('button', { name: 'Send' });
    expect(send).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Attach files' }));
    expect(await screen.findByText('x.go')).toBeInTheDocument();
    expect(send).toBeEnabled();

    fireEvent.click(send);
    expect(onSend).toHaveBeenCalledWith('', ['/a/x.go']);
  });
});
