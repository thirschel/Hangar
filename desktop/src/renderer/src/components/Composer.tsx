import type { FormEvent, JSX, ReactNode } from 'react';
import { memo, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ModelInfo } from '../../../main/host-client';

/**
 * Composer is the message input for the Agent (rich Copilot) chat surface. It is
 * a self-contained, controlled text box plus an actions row -- a LIVE upload
 * (file attachments) button, a LIVE model selector (model + reasoning effort +
 * context tier, Copilot-style submenus), a Stop button while a turn streams, and
 * Send. The hosting ChatView renders it in a slot and owns send/stop plus the
 * model data (list + current model/effort/tier + the apply callback); Composer
 * manages its own draft text and attachment list (both cleared after a
 * successful send) and the model menu's open/closed + submenu state.
 *
 * Attachments come from the native multi-select file picker (window.cs.pickFiles)
 * and render as removable chips above the box; the list is de-duplicated by path.
 *
 * Submit is Enter (unless Shift is held or IME composition is active) or the
 * Send button. Sending is a no-op while a turn is in progress or when hard-
 * disabled via `disabledSend`; otherwise Send is enabled when there is trimmed
 * text OR at least one attachment (so files can be sent with no text).
 */
export type ComposerProps = {
  /** A turn is streaming: show Stop and disable Send. */
  turnInProgress: boolean;
  /** Hard-disable Send regardless of draft text (e.g. no active session). */
  disabledSend?: boolean;
  /**
   * Called with the trimmed draft text and the selected attachment paths
   * (absolute). At least one of the two is non-empty. Composer clears both its
   * draft and attachment list afterwards.
   */
  onSend: (text: string, attachments: string[]) => void;
  /** Abort the in-progress turn. */
  onStop: () => void;
  /** Right-aligned info shown ABOVE the box (model name + context %). */
  info?: ReactNode;
  /**
   * Left-aligned activity status shown ABOVE the box while a turn is in progress
   * (e.g. "Searching", "Reading", "Working"). When set, a small spinner is shown
   * next to it. Omit/undefined to hide the status (and spinner).
   */
  status?: string;
  /**
   * Models for the live selector. When empty/undefined (or no `onApplyModel`),
   * the Model button is a disabled placeholder.
   */
  models?: ModelInfo[];
  /** Id of the active model: marks the active items and drives the button label. */
  currentModelId?: string;
  /** Active reasoning effort (raw SDK value, e.g. "medium"); shown title-cased. */
  currentEffort?: string;
  /** Active context tier ('default' | 'long_context'). */
  currentContextTier?: string;
  /**
   * Apply a model + reasoning effort + context tier together (effort/contextTier
   * are raw values). Composer closes the menu after invoking it. When absent the
   * Model button is a disabled placeholder.
   */
  onApplyModel?: (modelId: string, effort: string, contextTier: string) => void;
};

// The two context tiers offered by the Context submenu. `value` is the raw tier
// sent to the daemon; `label` is the human-friendly menu text.
const CONTEXT_TIERS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'default', label: 'Default' },
  { value: 'long_context', label: 'Long context' },
];

// Which submenu (if any) is expanded inside the open model menu.
type ModelMenuSection = 'effort' | 'context' | 'models';

// Title-case a raw effort value for display: "medium" -> "Medium". Compact
// "x"-prefixed efforts read as e.g. "xhigh" -> "XHigh" (not "Xhigh").
function titleCaseEffort(effort: string): string {
  const lower = effort.toLowerCase();
  if (lower.length === 0) return '';
  if (lower.startsWith('x') && lower.length > 1) {
    return `X${lower.charAt(1).toUpperCase()}${lower.slice(2)}`;
  }
  return `${lower.charAt(0).toUpperCase()}${lower.slice(1)}`;
}

// Display label for a context tier value (falls back to the raw value).
function contextTierLabel(tier: string): string {
  return CONTEXT_TIERS.find((entry) => entry.value === tier)?.label ?? tier;
}

// Chip label for an attachment: the file's basename (handles both Windows and
// POSIX separators), falling back to the full path when there is no separator.
function basename(filePath: string): string {
  const segments = filePath.split(/[\\/]/);
  return segments[segments.length - 1] || filePath;
}

// A tiny 3x3 "cube grid" spinner (sk-cube-grid) shown next to the activity status
// while a turn streams. Pure CSS animation; honors prefers-reduced-motion.
function StatusSpinner(): JSX.Element {
  return (
    <span className="chat-status-spinner" aria-hidden="true">
      {Array.from({ length: 9 }, (_, i) => (
        <span key={i} className={`sk-cube sk-cube${i + 1}`} />
      ))}
    </span>
  );
}

function ComposerView({
  turnInProgress,
  disabledSend = false,
  onSend,
  onStop,
  status,
  info,
  models,
  currentModelId,
  currentEffort,
  currentContextTier,
  onApplyModel,
}: ComposerProps): JSX.Element {
  const [text, setText] = useState('');
  const [attachments, setAttachments] = useState<string[]>([]);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [openSection, setOpenSection] = useState<ModelMenuSection | null>(null);
  const [escArmed, setEscArmed] = useState(false);
  const modelRef = useRef<HTMLDivElement>(null);
  const submenuRef = useRef<HTMLDivElement>(null);
  const escTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const trimmed = text.trim();
  // Send is allowed with trimmed text OR at least one attachment (a files-only
  // send), but never while a turn streams or when hard-disabled.
  const canSend =
    (trimmed.length > 0 || attachments.length > 0) && !turnInProgress && !disabledSend;

  const modelList = models ?? [];
  // The selector is live only when there is something to pick and a way to apply
  // it; otherwise the button stays a disabled placeholder (matches Upload).
  const modelSelectable = modelList.length > 0 && onApplyModel !== undefined;
  const currentModel = modelList.find((model) => model.id === currentModelId);
  const supportedEfforts = currentModel?.supportedEfforts ?? [];
  const supportsEffort = supportedEfforts.length > 0;
  // Effort/context fall back to the model default / 'default' so the button label
  // and submenu checks render sensibly even before the host seeds them.
  const effort = currentEffort ?? currentModel?.defaultEffort ?? '';
  const contextTier = currentContextTier ?? 'default';
  const modelName = currentModel?.name ?? currentModel?.id ?? currentModelId ?? 'Model';

  // Close the menu (and collapse any open submenu) on an outside click or Escape
  // while it is open. Bound only while open so we don't keep document listeners
  // around for an idle composer.
  useEffect(() => {
    if (!modelMenuOpen) return undefined;
    const close = (): void => {
      setModelMenuOpen(false);
      setOpenSection(null);
    };
    const onPointerDown = (event: MouseEvent): void => {
      if (modelRef.current && !modelRef.current.contains(event.target as Node)) {
        close();
      }
    };
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') close();
    };
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [modelMenuOpen]);

  // Keep the open flyout submenu inside the window: it opens to the left of the
  // menu by default, but a narrow window (or a wide list) can push it off an edge.
  // Measure the un-clamped rect and nudge it back into view via an inline
  // transform (the submenu's animation is opacity-only so it won't fight this).
  useLayoutEffect(() => {
    const el = submenuRef.current;
    if (!el || openSection === null) return;
    el.style.transform = 'none';
    const rect = el.getBoundingClientRect();
    const margin = 8;
    let dx = 0;
    let dy = 0;
    if (rect.left < margin) {
      dx = margin - rect.left;
    } else if (rect.right > window.innerWidth - margin) {
      dx = window.innerWidth - margin - rect.right;
    }
    if (rect.bottom > window.innerHeight - margin) {
      dy = window.innerHeight - margin - rect.bottom;
    } else if (rect.top < margin) {
      dy = margin - rect.top;
    }
    if (dx !== 0 || dy !== 0) {
      el.style.transform = `translate(${dx}px, ${dy}px)`;
    }
  }, [openSection]);

  useEffect(() => {
    if (!turnInProgress) {
      if (escTimerRef.current !== null) {
        clearTimeout(escTimerRef.current);
        escTimerRef.current = null;
      }
      setEscArmed(false);
    }

    return () => {
      if (escTimerRef.current !== null) {
        clearTimeout(escTimerRef.current);
        escTimerRef.current = null;
      }
    };
  }, [turnInProgress]);

  const disarmEsc = (): void => {
    if (escTimerRef.current !== null) {
      clearTimeout(escTimerRef.current);
      escTimerRef.current = null;
    }
    setEscArmed(false);
  };

  // Send the current draft and clear the box. No-op unless `canSend`, so the
  // keyboard shortcut and the Send button share one guard.
  const trySend = (): void => {
    if (!canSend) return;
    disarmEsc();
    onSend(trimmed, attachments);
    setText('');
    setAttachments([]);
  };

  // Open the native multi-select picker and append the chosen paths, skipping
  // any already present (de-duplicate by absolute path). A canceled picker
  // returns [] and a failing one is swallowed -- neither disturbs the current list.
  const addAttachments = (): void => {
    void window.cs
      .pickFiles()
      .then((paths) => {
        if (paths.length === 0) return;
        setAttachments((current) => {
          const seen = new Set(current);
          const next = [...current];
          for (const path of paths) {
            if (!seen.has(path)) {
              seen.add(path);
              next.push(path);
            }
          }
          return next;
        });
      })
      .catch(() => {
        /* Picker unavailable/failed: keep the current attachments untouched. */
      });
  };

  const removeAttachment = (path: string): void => {
    setAttachments((current) => current.filter((entry) => entry !== path));
  };

  const onSubmit = (event: FormEvent): void => {
    event.preventDefault();
    trySend();
  };

  const closeModelMenu = (): void => {
    setModelMenuOpen(false);
    setOpenSection(null);
  };

  const toggleModelMenu = (): void => {
    if (modelMenuOpen) {
      closeModelMenu();
    } else {
      setModelMenuOpen(true);
    }
  };

  // Expand the clicked submenu (or collapse it if it is already open).
  const toggleSection = (section: ModelMenuSection): void => {
    setOpenSection((current) => (current === section ? null : section));
  };

  // Selecting an effort keeps the current model + context tier; a context tier
  // keeps the current model + effort; a different model resets the effort to that
  // model's default (and keeps the current context tier). Each closes the menu.
  const chooseEffort = (value: string): void => {
    if (currentModelId !== undefined) onApplyModel?.(currentModelId, value, contextTier);
    closeModelMenu();
  };

  const chooseContext = (value: string): void => {
    if (currentModelId !== undefined) onApplyModel?.(currentModelId, effort, value);
    closeModelMenu();
  };

  const chooseModel = (model: ModelInfo): void => {
    onApplyModel?.(model.id, model.defaultEffort ?? '', contextTier);
    closeModelMenu();
  };

  return (
    <form className="chat-composer" onSubmit={onSubmit}>
      {(status !== undefined || info !== undefined) && (
        <div className="chat-composer__header">
          {status !== undefined ? (
            <div className="chat-composer__status">
              <StatusSpinner />
              <span className="chat-composer__status-label">{status}</span>
              {escArmed && (
                <span className="chat-composer__esc-hint">
                  hit esc one more time to cancel
                </span>
              )}
            </div>
          ) : (
            <span />
          )}
          {info !== undefined ? <div className="chat-composer__info">{info}</div> : <span />}
        </div>
      )}
      <div className="chat-composer__box">
        {attachments.length > 0 && (
          <ul className="chat-composer__attachments" aria-label="Attachments">
            {attachments.map((path) => (
              <li key={path} className="chat-composer__chip">
                <span className="chat-composer__chip-name" title={path}>
                  {basename(path)}
                </span>
                <button
                  type="button"
                  className="chat-composer__chip-remove"
                  title={`Remove ${basename(path)}`}
                  aria-label={`Remove ${basename(path)}`}
                  onClick={() => removeAttachment(path)}
                >
                  <span aria-hidden="true">{'\u00D7'}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
        <textarea
          className="chat-composer__input"
          value={text}
          placeholder={'Message Copilot\u2026'}
          rows={3}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              if (modelMenuOpen) return;
              if (!turnInProgress) return;

              event.preventDefault();
              if (escArmed) {
                disarmEsc();
                onStop();
                return;
              }

              setEscArmed(true);
              escTimerRef.current = setTimeout(() => {
                escTimerRef.current = null;
                setEscArmed(false);
              }, 2000);
              return;
            }

            if (
              event.key === 'Enter' &&
              !event.shiftKey &&
              !event.nativeEvent.isComposing
            ) {
              event.preventDefault();
              trySend();
            }
          }}
        />
        <div className="chat-composer__actions">
          <button
            type="button"
            className="chat-composer__tool chat-composer__upload"
            title="Attach files"
            aria-label="Attach files"
            onClick={addAttachments}
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
              onClick={toggleModelMenu}
            >
              <span className="chat-composer__model-name">{modelName}</span>
              {supportsEffort && effort.length > 0 && (
                <span className="chat-composer__model-effort">{titleCaseEffort(effort)}</span>
              )}
              <span className="chat-composer__model-caret" aria-hidden="true">
                {'\u2304'}
              </span>
            </button>
            {modelMenuOpen && modelSelectable && (
              <div className="chat-composer__model-menu" role="menu" aria-label="Select model">
                {currentModelId !== undefined && (
                  <div className="chat-composer__model-current" role="presentation">
                    <span className="chat-composer__model-current-name">{modelName}</span>
                    <span className="chat-composer__model-check" aria-hidden="true">
                      {'\u2713'}
                    </span>
                  </div>
                )}

                {supportsEffort && (
                  <div className="chat-composer__model-group">
                    <button
                      type="button"
                      role="menuitem"
                      aria-haspopup="menu"
                      aria-expanded={openSection === 'effort'}
                      className="chat-composer__model-row"
                      onClick={() => toggleSection('effort')}
                    >
                      <span className="chat-composer__model-row-label">Effort</span>
                      <span className="chat-composer__model-row-value">
                        {titleCaseEffort(effort)}
                      </span>
                      <span className="chat-composer__model-row-caret" aria-hidden="true">
                        {'\u203A'}
                      </span>
                    </button>
                    {openSection === 'effort' && (
                      <div className="chat-composer__submenu" role="menu" aria-label="Effort" ref={submenuRef}>
                        {supportedEfforts.map((value) => {
                          const active = value === effort;
                          return (
                            <button
                              key={value}
                              type="button"
                              role="menuitemradio"
                              aria-checked={active}
                              className={
                                active
                                  ? 'chat-composer__submenu-item chat-composer__submenu-item--active'
                                  : 'chat-composer__submenu-item'
                              }
                              onClick={() => chooseEffort(value)}
                            >
                              {titleCaseEffort(value)}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}

                {currentModelId !== undefined && (
                  <div className="chat-composer__model-group">
                    <button
                      type="button"
                      role="menuitem"
                      aria-haspopup="menu"
                      aria-expanded={openSection === 'context'}
                      className="chat-composer__model-row"
                      onClick={() => toggleSection('context')}
                    >
                      <span className="chat-composer__model-row-label">Context</span>
                      <span className="chat-composer__model-row-value">
                        {contextTierLabel(contextTier)}
                      </span>
                      <span className="chat-composer__model-row-caret" aria-hidden="true">
                        {'\u203A'}
                      </span>
                    </button>
                    {openSection === 'context' && (
                      <div className="chat-composer__submenu" role="menu" aria-label="Context" ref={submenuRef}>
                        {CONTEXT_TIERS.map((entry) => {
                          const active = entry.value === contextTier;
                          return (
                            <button
                              key={entry.value}
                              type="button"
                              role="menuitemradio"
                              aria-checked={active}
                              className={
                                active
                                  ? 'chat-composer__submenu-item chat-composer__submenu-item--active'
                                  : 'chat-composer__submenu-item'
                              }
                              onClick={() => chooseContext(entry.value)}
                            >
                              {entry.label}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}

                <div className="chat-composer__model-group">
                  <button
                    type="button"
                    role="menuitem"
                    aria-haspopup="menu"
                    aria-expanded={openSection === 'models'}
                    className="chat-composer__model-row"
                    onClick={() => toggleSection('models')}
                  >
                    <span className="chat-composer__model-row-label">More models</span>
                    <span className="chat-composer__model-row-caret" aria-hidden="true">
                      {'\u203A'}
                    </span>
                  </button>
                  {openSection === 'models' && (
                    <div className="chat-composer__submenu" role="menu" aria-label="Models" ref={submenuRef}>
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
                                ? 'chat-composer__submenu-item chat-composer__submenu-item--active'
                                : 'chat-composer__submenu-item'
                            }
                            onClick={() => chooseModel(model)}
                          >
                            {model.name ?? model.id}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
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
