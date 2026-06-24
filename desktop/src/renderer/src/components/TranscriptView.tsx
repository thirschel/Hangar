import type { FormEvent, JSX } from 'react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { EventFrame } from '../../../main/host-client';

type TranscriptViewProps = {
  sessionName: string;
};

type TranscriptEntry =
  | { id: string; kind: 'assistant'; text: string; streaming: boolean }
  | { id: string; kind: 'reasoning'; text: string }
  | {
      id: string;
      kind: 'tool';
      toolName: string;
      mcpServer?: string;
      status: 'running' | 'done';
      detail?: string;
    }
  | { id: string; kind: 'permission'; question: string; choices: string[] }
  | { id: string; kind: 'input'; question: string; choices: string[] }
  | { id: string; kind: 'usage'; text: string }
  | { id: string; kind: 'idle'; aborted: boolean }
  | { id: string; kind: 'error'; text: string };

type TranscriptModel = {
  entries: TranscriptEntry[];
  title: string;
  turnInProgress: boolean;
};

const SCROLL_SLOP = 48;

function frameText(frame: EventFrame): string {
  return frame.text ?? frame.error ?? frame.status ?? '';
}

function toolKey(frame: EventFrame): string {
  return frame.requestId ?? `${frame.toolName ?? 'tool'}:${frame.mcpServer ?? ''}`;
}

function buildTranscript(frames: EventFrame[]): TranscriptModel {
  const entries: TranscriptEntry[] = [];
  const toolEntries = new Map<string, number>();
  let pendingAssistantIndex: number | null = null;
  let pendingAssistantText = '';
  let title = 'Transcript';
  let turnInProgress = false;

  for (const frame of frames) {
    switch (frame.kind) {
      case 'assistant.delta': {
        pendingAssistantText += frame.text ?? '';
        turnInProgress = true;
        if (pendingAssistantIndex === null) {
          pendingAssistantIndex = entries.length;
          entries.push({
            id: `assistant-stream-${frame.seq}`,
            kind: 'assistant',
            text: pendingAssistantText,
            streaming: true,
          });
        } else {
          entries[pendingAssistantIndex] = {
            ...entries[pendingAssistantIndex],
            kind: 'assistant',
            text: pendingAssistantText,
            streaming: true,
          } as TranscriptEntry;
        }
        break;
      }
      case 'assistant.message': {
        const text = frame.text ?? pendingAssistantText;
        if (pendingAssistantIndex !== null) {
          entries[pendingAssistantIndex] = {
            id: `assistant-${frame.seq}`,
            kind: 'assistant',
            text,
            streaming: false,
          };
        } else {
          entries.push({ id: `assistant-${frame.seq}`, kind: 'assistant', text, streaming: false });
        }
        pendingAssistantIndex = null;
        pendingAssistantText = '';
        turnInProgress = false;
        break;
      }
      case 'assistant.reasoning':
        turnInProgress = true;
        entries.push({ id: `reasoning-${frame.seq}`, kind: 'reasoning', text: frameText(frame) });
        break;
      case 'tool.start': {
        turnInProgress = true;
        const entry: TranscriptEntry = {
          id: `tool-${toolKey(frame)}-${frame.seq}`,
          kind: 'tool',
          toolName: frame.toolName ?? 'Tool',
          mcpServer: frame.mcpServer,
          status: 'running',
          detail: frame.status,
        };
        toolEntries.set(toolKey(frame), entries.length);
        entries.push(entry);
        break;
      }
      case 'tool.complete': {
        const key = toolKey(frame);
        const index = toolEntries.get(key);
        const next: TranscriptEntry = {
          id: `tool-${key}-${frame.seq}`,
          kind: 'tool',
          toolName: frame.toolName ?? 'Tool',
          mcpServer: frame.mcpServer,
          status: 'done',
          detail: frame.error ?? frame.status,
        };
        if (index === undefined) entries.push(next);
        else entries[index] = { ...next, id: entries[index].id };
        break;
      }
      case 'permission.requested':
        turnInProgress = true;
        entries.push({
          id: `permission-${frame.seq}`,
          kind: 'permission',
          question: frame.question ?? frame.text ?? 'Permission requested',
          choices: frame.choices ?? [],
        });
        break;
      case 'user_input.requested':
        turnInProgress = true;
        entries.push({
          id: `input-${frame.seq}`,
          kind: 'input',
          question: frame.question ?? frame.text ?? 'Input requested',
          choices: frame.choices ?? [],
        });
        break;
      case 'usage':
        entries.push({ id: `usage-${frame.seq}`, kind: 'usage', text: frameText(frame) });
        break;
      case 'title':
        title = frame.title ?? frame.text ?? title;
        break;
      case 'idle':
        if (pendingAssistantIndex !== null) {
          entries[pendingAssistantIndex] = {
            ...entries[pendingAssistantIndex],
            kind: 'assistant',
            streaming: false,
          } as TranscriptEntry;
          pendingAssistantIndex = null;
          pendingAssistantText = '';
        }
        turnInProgress = false;
        entries.push({ id: `idle-${frame.seq}`, kind: 'idle', aborted: frame.aborted ?? false });
        break;
      case 'error':
        turnInProgress = false;
        entries.push({ id: `error-${frame.seq}`, kind: 'error', text: frameText(frame) || 'Error' });
        break;
      default:
        break;
    }
  }

  return { entries, title, turnInProgress };
}

export function TranscriptView({ sessionName }: TranscriptViewProps): JSX.Element {
  const [framesBySeq, setFramesBySeq] = useState<Map<number, EventFrame>>(() => new Map());
  const [composerText, setComposerText] = useState('');
  const [streamError, setStreamError] = useState<string | null>(null);
  const [optimisticTurn, setOptimisticTurn] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const shouldStickToBottom = useRef(true);

  const frames = useMemo(
    () => Array.from(framesBySeq.values()).sort((a, b) => a.seq - b.seq),
    [framesBySeq],
  );
  const transcript = useMemo(() => buildTranscript(frames), [frames]);
  const turnInProgress = transcript.turnInProgress || optimisticTurn;

  useEffect(() => {
    setFramesBySeq(new Map());
    setStreamError(null);
    setOptimisticTurn(false);
    shouldStickToBottom.current = true;

    const addFrame = (frame: EventFrame): void => {
      setFramesBySeq((current) => {
        if (current.get(frame.seq) === frame) return current;
        const next = new Map(current);
        next.set(frame.seq, frame);
        return next;
      });
      if (frame.kind === 'idle' || frame.kind === 'error' || frame.kind === 'assistant.message') {
        setOptimisticTurn(false);
      }
    };

    const unsubscribeFrame = window.cs.onRichFrame(({ session, frame }) => {
      if (session === sessionName) addFrame(frame);
    });
    const unsubscribeError = window.cs.onRichError(({ session, message }) => {
      if (session === sessionName) setStreamError(message);
    });
    void window.cs.openRichStream(sessionName, 0).catch((error: unknown) => {
      setStreamError(error instanceof Error ? error.message : String(error));
    });

    return () => {
      unsubscribeFrame();
      unsubscribeError();
      void window.cs.closeRichStream(sessionName);
    };
  }, [sessionName]);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && shouldStickToBottom.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [frames.length, streamError]);

  const onScroll = (): void => {
    const el = scrollRef.current;
    if (!el) return;
    shouldStickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_SLOP;
  };

  const send = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    const message = composerText.trim();
    if (!message || turnInProgress) return;
    setComposerText('');
    setOptimisticTurn(true);
    try {
      await window.cs.sendMessage(sessionName, message);
    } catch (error) {
      setOptimisticTurn(false);
      setStreamError(error instanceof Error ? error.message : String(error));
    }
  };

  const stop = async (): Promise<void> => {
    try {
      await window.cs.abortTurn(sessionName);
      setOptimisticTurn(false);
    } catch (error) {
      setStreamError(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <div className="transcript-view">
      <div className="transcript-view__header">
        <div>
          <div className="transcript-view__eyebrow">Rich agent view</div>
          <h2>{transcript.title}</h2>
        </div>
        {turnInProgress && <span className="transcript-view__live">Streaming…</span>}
      </div>

      <div ref={scrollRef} className="transcript-view__scroll" onScroll={onScroll}>
        {transcript.entries.length === 0 && !streamError && (
          <div className="transcript-view__empty">Waiting for Copilot transcript events…</div>
        )}
        {transcript.entries.map((entry) => (
          <TranscriptEntryView key={entry.id} entry={entry} />
        ))}
        {streamError && (
          <div className="transcript-entry transcript-entry--error" role="alert">
            {streamError}
          </div>
        )}
      </div>

      <form className="transcript-composer" onSubmit={send}>
        <textarea
          value={composerText}
          placeholder="Message Copilot…"
          rows={3}
          onChange={(event) => setComposerText(event.target.value)}
          onKeyDown={(event) => {
            if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
              void send(event);
            }
          }}
        />
        <div className="transcript-composer__actions">
          {turnInProgress && (
            <button type="button" className="transcript-composer__stop" onClick={() => void stop()}>
              Stop
            </button>
          )}
          <button type="submit" disabled={!composerText.trim() || turnInProgress}>
            Send
          </button>
        </div>
      </form>
    </div>
  );
}

function TranscriptEntryView({ entry }: { entry: TranscriptEntry }): JSX.Element {
  switch (entry.kind) {
    case 'assistant':
      return (
        <article className="transcript-entry transcript-entry--assistant">
          <div className="transcript-entry__label">Assistant</div>
          <div className="transcript-entry__text">{entry.text}</div>
          {entry.streaming && <span className="transcript-entry__cursor" aria-label="streaming" />}
        </article>
      );
    case 'reasoning':
      return (
        <details className="transcript-entry transcript-entry--reasoning">
          <summary>Reasoning</summary>
          <div className="transcript-entry__text">{entry.text}</div>
        </details>
      );
    case 'tool':
      return (
        <div className={`transcript-tool transcript-tool--${entry.status}`}>
          <span className="transcript-tool__state">{entry.status === 'running' ? 'Running' : 'Done'}</span>
          <span className="transcript-tool__name">{entry.toolName}</span>
          {entry.mcpServer && <span className="transcript-tool__server">{entry.mcpServer}</span>}
          {entry.detail && <span className="transcript-tool__detail">{entry.detail}</span>}
        </div>
      );
    case 'permission':
      return (
        <div className="transcript-entry transcript-entry--permission">
          <strong>Permission requested</strong>
          <span>{entry.question}</span>
          {entry.choices.length > 0 && <span className="transcript-entry__meta">{entry.choices.join(' / ')}</span>}
        </div>
      );
    case 'input':
      return (
        <div className="transcript-entry transcript-entry--permission">
          <strong>Input requested</strong>
          <span>{entry.question}</span>
        </div>
      );
    case 'usage':
      return <div className="transcript-entry transcript-entry--usage">{entry.text || 'Usage updated'}</div>;
    case 'idle':
      return entry.aborted ? (
        <div className="transcript-entry transcript-entry--usage">Turn aborted.</div>
      ) : (
        <div className="transcript-entry transcript-entry--usage">Turn complete.</div>
      );
    case 'error':
      return (
        <div className="transcript-entry transcript-entry--error" role="alert">
          {entry.text}
        </div>
      );
  }
}
