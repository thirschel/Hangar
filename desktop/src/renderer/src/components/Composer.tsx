import type { FormEvent, JSX, ReactNode } from 'react';
import { memo, useEffect, useRef, useState } from 'react';
import type { ModelInfo } from '../../../main/host-client';

/**
 * Composer is the message input for the Agent (rich Copilot) chat surface. It is
 * a self-contained, controlled text box plus an actions row -- a disabled upload
 * placeholder, a LIVE model selector, a Stop button while a turn streams, and
 * Send. The hosting ChatView renders it in a slot and owns send/stop plus the
 * model data (list + current + switch callback); Composer manages its own draft
 * text (cleared after a successful send) and the model menu's open/closed state.
 *
 * Submit is Ctrl/Cmd+Enter (mirrors TranscriptView) or the Send button; plain
 * Enter inserts a newline. Sending is a no-op while a turn is in progress, when
 * the draft is empty/whitespace, or when hard-disabled via `disabledSend`.
 */
export type ComposerProps = {
  /** A turn is streaming: show Stop and disable Send. */
  turnInProgress: boolean;
  /** Hard-disable Send regardless of draft text (e.g. no active session). */
  disabledSend?: boolean;
  /** Called with trimmed, non-empty text; Composer clears its draft afterwards. */
  onSend: (text: string) => void;
  /** Abort the in-progress turn. */
  onStop: () => void;
  /** Right-aligned info shown ABOVE the box (model name + context %). */
  info?: ReactNode;
  /**
   * Models for the live selector dropdown. When empty/undefined (or no
   * `onSelectModel`), the Model button is a disabled placeholder.
   */
  models?: ModelInfo[];
  /** Id of the active model: marks the active menu item and drives the button label. */
  currentModelId?: string;
  /** Switch the session's model; Composer closes the menu after invoking it. */
  onSelectModel?: (id: string) => void;
};

function modelButtonLabel(models: ModelInfo[], currentModelId?: string): string {
  if (!currentModelId) return 'Model';
  const current = models.find((model) => model.id === currentModelId);
  return current?.name ?? current?.id ?? currentModelId;
}

function ComposerView({
  turnInProgress,
  disabledSend = false,
  onSend,
  onStop,
  info,
  models,
  currentModelId,
  onSelectModel,
}: ComposerProps): JSX.Element {
  const [text, setText] = useState('');
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const modelRef = useRef<HTMLDivElement>(null);
  const trimmed = text.trim();
  const canSend = trimmed.length > 0 && !turnInProgress && !disabledSend;

  const modelList = models ?? [];
  // The selector is live only when there is something to pick and a way to apply
  // it; otherwise the button stays a disabled placeholder (matches Upload).
  const modelSelectable = modelList.length > 0 && onSelectModel !== undefined;
  const modelLabel = modelButtonLabel(modelList, currentModelId);

  // Close the menu on an outside click or Escape while it is open. Bound only
  // while open so we don't keep document listeners around for an idle composer.
  useEffect(() => {
    if (!modelMenuOpen) return undefined;
    const onPointerDown = (event: MouseEvent): void => {
      if (modelRef.current && !modelRef.current.contains(event.target as Node)) {
        setModelMenuOpen(false);
      }
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') setModelMenuOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [modelMenuOpen]);

  // Send the current draft and clear the box. No-op unless `canSend`, so the
  // keyboard shortcut and the Send button share one guard.
  const trySend = (): void => {
    if (!canSend) return;
    onSend(trimmed);
    setText('');
  };

  const onSubmit = (event: FormEvent): void => {
    event.preventDefault();
    trySend();
  };

  const chooseModel = (id: string): void => {
    setModelMenuOpen(false);
    onSelectModel?.(id);
  };

  return (
    <form className="chat-composer" onSubmit={onSubmit}>
      {info !== undefined && <div className="chat-composer__info">{info}</div>}
      <div className="chat-composer__box">
        <textarea
          className="chat-composer__input"
          value={text}
          placeholder={'Message Copilot\u2026'}
          rows={3}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
              event.preventDefault();
              trySend();
            }
          }}
        />
        <div className="chat-composer__actions">
          <button
            type="button"
            className="chat-composer__tool chat-composer__upload"
            disabled
            title={'Attachments \u2014 coming soon'}
            aria-label={'Attachments \u2014 coming soon'}
          >
            <span aria-hidden="true">{'\uD83D\uDCCE'}</span>
          </button>
          <div className="chat-composer__model-wrap" ref={modelRef}>
            <button
              type="button"
              className="chat-composer__tool chat-composer__model"
              disabled={!modelSelectable}
              aria-haspopup="menu"
              aria-expanded={modelMenuOpen}
              title={modelSelectable ? 'Select model' : 'No models available'}
              onClick={() => setModelMenuOpen((open) => !open)}
            >
              <span aria-hidden="true">{'\u2304'}</span> {modelLabel}
            </button>
            {modelMenuOpen && modelSelectable && (
              <div className="chat-composer__model-menu" role="menu" aria-label="Select model">
                {modelList.map((model) => {
                  const active = model.id === currentModelId;
                  return (
                    <button
                      key={model.id}
                      type="button"
                      role="menuitemradio"
                      aria-checked={active}
                      className={
                        active
                          ? 'chat-composer__model-item chat-composer__model-item--active'
                          : 'chat-composer__model-item'
                      }
                      onClick={() => chooseModel(model.id)}
                    >
                      {model.name ?? model.id}
                    </button>
                  );
                })}
              </div>
            )}
          </div>
          {turnInProgress && (
            <button type="button" className="chat-composer__stop" onClick={onStop}>
              Stop
            </button>
          )}
          <button type="submit" className="chat-composer__send" disabled={!canSend}>
            Send
          </button>
        </div>
      </div>
    </form>
  );
}

export const Composer = memo(ComposerView);
