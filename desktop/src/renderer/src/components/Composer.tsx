import type { FormEvent, JSX, ReactNode } from 'react';
import { memo, useState } from 'react';

/**
 * Composer is the message input for the Agent (rich Copilot) chat surface. It is
 * a self-contained, controlled text box plus an actions row -- upload + model
 * selector placeholders, a Stop button while a turn streams, and Send. The
 * hosting ChatView renders it in a slot and owns send/stop; Composer only manages
 * its own draft text and clears it after a successful send.
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
  /** Right-aligned info shown ABOVE the box (model, context% -- placeholders). */
  info?: ReactNode;
};

function ComposerView({
  turnInProgress,
  disabledSend = false,
  onSend,
  onStop,
  info,
}: ComposerProps): JSX.Element {
  const [text, setText] = useState('');
  const trimmed = text.trim();
  const canSend = trimmed.length > 0 && !turnInProgress && !disabledSend;

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
          <button
            type="button"
            className="chat-composer__tool chat-composer__model"
            disabled
            title={'Model selector \u2014 coming soon'}
          >
            <span aria-hidden="true">{'\u2304'}</span> Model
          </button>
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
