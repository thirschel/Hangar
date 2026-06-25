// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

  it('opens the model menu, shows the current model with a check, and lists all via More models', () => {
    const onApplyModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [
            { id: 'gpt-5', name: 'GPT-5' },
            { id: 'claude', name: 'Claude' },
          ],
          currentModelId: 'gpt-5',
          onApplyModel,
        })}
      />,
    );

    // The button label reflects the current model; the menu is closed initially.
    const modelButton = screen.getByRole('button', { name: /GPT-5/ });
    expect(modelButton).toBeEnabled();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();

    fireEvent.click(modelButton);
    const menu = screen.getByRole('menu', { name: 'Select model' });
    expect(menu).toBeInTheDocument();
    // The current model is named in the header row (a check sits beside it).
    expect(within(menu).getByText('GPT-5')).toBeInTheDocument();

    // Expand More models to see every model; the active one is checked.
    fireEvent.click(screen.getByRole('menuitem', { name: /More models/ }));
    expect(screen.getByRole('menuitemradio', { name: 'GPT-5' })).toHaveAttribute(
      'aria-checked',
      'true',
    );
    expect(screen.getByRole('menuitemradio', { name: 'Claude' })).toHaveAttribute(
      'aria-checked',
      'false',
    );
  });

  it('switches the model via More models, resetting effort to the model default', () => {
    const onApplyModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [
            { id: 'gpt-5', name: 'GPT-5' },
            { id: 'claude', name: 'Claude', defaultEffort: 'high' },
          ],
          currentModelId: 'gpt-5',
          onApplyModel,
        })}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /GPT-5/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /More models/ }));
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'Claude' }));

    // A different model applies with that model's default effort + the current tier.
    expect(onApplyModel).toHaveBeenCalledWith('claude', 'high', 'default');
    // Selecting closes the menu.
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
  });

  it('renders the Effort submenu for a model that supports efforts and applies a pick', () => {
    const onApplyModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [
            {
              id: 'sonnet',
              name: 'Sonnet 4.6',
              supportedEfforts: ['low', 'medium', 'high'],
              defaultEffort: 'medium',
            },
          ],
          currentModelId: 'sonnet',
          currentEffort: 'medium',
          onApplyModel,
        })}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /Sonnet 4.6/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Effort/ }));

    // The supported efforts render title-cased, with the active one checked.
    const effortMenu = screen.getByRole('menu', { name: 'Effort' });
    expect(within(effortMenu).getByRole('menuitemradio', { name: 'Low' })).toBeInTheDocument();
    expect(within(effortMenu).getByRole('menuitemradio', { name: 'Medium' })).toHaveAttribute(
      'aria-checked',
      'true',
    );
    expect(within(effortMenu).getByRole('menuitemradio', { name: 'High' })).toBeInTheDocument();

    fireEvent.click(within(effortMenu).getByRole('menuitemradio', { name: 'High' }));
    // An effort pick keeps the current model + context tier; the raw value is sent.
    expect(onApplyModel).toHaveBeenCalledWith('sonnet', 'high', 'default');
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
  });

  it('hides the Effort row for a model with no supported efforts', () => {
    render(
      <Composer
        {...baseProps({
          models: [{ id: 'mini', name: 'Mini' }],
          currentModelId: 'mini',
          onApplyModel: vi.fn(),
        })}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /Mini/ }));
    expect(screen.queryByRole('menuitem', { name: /Effort/ })).not.toBeInTheDocument();
    // Context + More models remain available.
    expect(screen.getByRole('menuitem', { name: /Context/ })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: /More models/ })).toBeInTheDocument();
  });

  it('offers Default / Long context in the Context submenu and applies a tier', () => {
    const onApplyModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [
            { id: 'sonnet', name: 'Sonnet', supportedEfforts: ['low', 'high'], defaultEffort: 'low' },
          ],
          currentModelId: 'sonnet',
          currentEffort: 'high',
          currentContextTier: 'default',
          onApplyModel,
        })}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: /Sonnet/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Context/ }));

    const contextMenu = screen.getByRole('menu', { name: 'Context' });
    expect(within(contextMenu).getByRole('menuitemradio', { name: 'Default' })).toHaveAttribute(
      'aria-checked',
      'true',
    );

    fireEvent.click(within(contextMenu).getByRole('menuitemradio', { name: 'Long context' }));
    // A context pick keeps the current model + effort; the raw tier is sent.
    expect(onApplyModel).toHaveBeenCalledWith('sonnet', 'high', 'long_context');
  });

  it('shows the model name and the title-cased effort on the button', () => {
    render(
      <Composer
        {...baseProps({
          models: [
            {
              id: 'sonnet',
              name: 'Sonnet 4.6',
              supportedEfforts: ['low', 'medium', 'high'],
              defaultEffort: 'medium',
            },
          ],
          currentModelId: 'sonnet',
          currentEffort: 'medium',
          onApplyModel: vi.fn(),
        })}
      />,
    );

    const button = screen.getByRole('button', { name: /Sonnet 4.6/ });
    expect(button).toHaveTextContent('Sonnet 4.6');
    expect(button).toHaveTextContent('Medium');
  });

  it('falls back to the model id as the label in More models', () => {
    render(<Composer {...baseProps({ models: [{ id: 'bare-id' }], onApplyModel: vi.fn() })} />);
    fireEvent.click(screen.getByRole('button', { name: /Model/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: /More models/ }));
    expect(screen.getByRole('menuitemradio', { name: 'bare-id' })).toBeInTheDocument();
  });

  it('closes the model menu on Escape without applying', () => {
    const onApplyModel = vi.fn();
    render(
      <Composer
        {...baseProps({
          models: [{ id: 'gpt-5', name: 'GPT-5' }],
          currentModelId: 'gpt-5',
          onApplyModel,
        })}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: /GPT-5/ }));
    expect(screen.getByRole('menu', { name: 'Select model' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Select model' })).not.toBeInTheDocument();
    expect(onApplyModel).not.toHaveBeenCalled();
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
