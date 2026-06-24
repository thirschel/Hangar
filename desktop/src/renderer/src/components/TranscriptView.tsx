import type { FormEvent, JSX } from 'react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { EventFrame } from '../../../main/host-client';

type TranscriptViewProps = {
  sessionName: string;
  autoYes?: boolean;
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
  | { id: string; kind: 'permission'; requestId?: string; question: string; choices: string[] }
  | { id: string; kind: 'input'; requestId?: string; question: string; choices: string[] }
  | { id: string; kind: 'usage'; text: string }
  | { id: string; kind: 'idle'; aborted: boolean }
  | { id: string; kind: 'error'; text: string };

type TranscriptModel = {
  entries: TranscriptEntry[];
  servers: Map<string, { status: string; error?: string }>;
  title: string;
  turnInProgress: boolean;
};

const SCROLL_SLOP = 48;
const MCP_STATUS_META: Record<string, { label: string; className: string }> = {
  connected: { label: 'Connected', className: 'ok' },
  failed: { label: 'Failed', className: 'error' },
  'needs-auth': { label: 'Needs auth', className: 'warn' },
  pending: { label: 'Pending', className: 'warn' },
  disabled: { label: 'Disabled', className: 'muted' },
  not_configured: { label: 'Not configured', className: 'muted' },
};

function frameText(frame: EventFrame): string {
  return frame.text ?? frame.error ?? frame.status ?? '';
}

function toolKey(frame: EventFrame): string {
  return frame.requestId ?? `${frame.toolName ?? 'tool'}:${frame.mcpServer ?? ''}`;
}

function buildTranscript(frames: EventFrame[]): TranscriptModel {
  const entries: TranscriptEntry[] = [];
  const servers = new Map<string, { status: string; error?: string }>();
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
          id: `permission-${frame.requestId ?? frame.seq}`,
          kind: 'permission',
          requestId: frame.requestId,
          question: frame.question ?? frame.text ?? 'Permission requested',
          choices: frame.choices ?? [],
        });
        break;
      case 'user_input.requested':
        turnInProgress = true;
        entries.push({
          id: `input-${frame.requestId ?? frame.seq}`,
          kind: 'input',
          requestId: frame.requestId,
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
      case 'mcp.status':
        if (frame.mcpServer) {
          servers.set(frame.mcpServer, { status: frame.status ?? 'pending', error: frame.error });
        }
        break;
      default:
        break;
    }
  }

  return { entries, servers, title, turnInProgress };
}

function mcpStatusMeta(status: string): { label: string; className: string } {
  return MCP_STATUS_META[status] ?? { label: status || 'Unknown', className: 'muted' };
}

export function TranscriptView({ sessionName, autoYes = false }: TranscriptViewProps): JSX.Element {
  const [framesBySeq, setFramesBySeq] = useState<Map<number, EventFrame>>(() => new Map());
  const [composerText, setComposerText] = useState('');
  const [streamError, setStreamError] = useState<string | null>(null);
  const [optimisticTurn, setOptimisticTurn] = useState(false);
  const [answeredRequests, setAnsweredRequests] = useState<Set<string>>(() => new Set());
  const [answerLabels, setAnswerLabels] = useState<Map<string, string>>(() => new Map());
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
    setAnsweredRequests(new Set());
    setAnswerLabels(new Map());
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

  const markAnswered = (requestId: string, label: string): void => {
    setAnsweredRequests((current) => {
      const next = new Set(current);
      next.add(requestId);
      return next;
    });
    setAnswerLabels((current) => {
      const next = new Map(current);
      next.set(requestId, label);
      return next;
    });
  };

  const unmarkAnswered = (requestId: string): void => {
    setAnsweredRequests((current) => {
      const next = new Set(current);
      next.delete(requestId);
      return next;
    });
    setAnswerLabels((current) => {
      const next = new Map(current);
      next.delete(requestId);
      return next;
    });
  };

  const respondPermission = async (
    requestId: string,
    decision: 'approve' | 'reject',
  ): Promise<void> => {
    markAnswered(requestId, decision === 'approve' ? 'approved' : 'rejected');
    try {
      await window.cs.respondPermission(sessionName, requestId, decision);
    } catch (error) {
      unmarkAnswered(requestId);
      setStreamError(error instanceof Error ? error.message : String(error));
    }
  };

  const respondUserInput = async (
    requestId: string,
    answer: string,
    wasFreeform: boolean,
  ): Promise<void> => {
    markAnswered(requestId, 'answered');
    try {
      await window.cs.respondUserInput(sessionName, requestId, answer, wasFreeform);
    } catch (error) {
      unmarkAnswered(requestId);
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

      {transcript.servers.size > 0 && (
        <div className="transcript-view__mcp" aria-label="MCP server status">
          <span className="transcript-view__mcp-title">MCP</span>
          <div className="transcript-view__mcp-list">
            {Array.from(transcript.servers.entries()).map(([server, info]) => {
              const meta = mcpStatusMeta(info.status);
              return (
                <span key={server} className="transcript-view__mcp-pill" title={info.error}>
                  <span className="transcript-view__mcp-server">{server}</span>
                  <span className={`transcript-view__mcp-status transcript-view__mcp-status--${meta.className}`}>
                    {meta.label}
                  </span>
                </span>
              );
            })}
          </div>
        </div>
      )}

      <div ref={scrollRef} className="transcript-view__scroll" onScroll={onScroll}>
        {transcript.entries.length === 0 && !streamError && (
          <div className="transcript-view__empty">Waiting for Copilot transcript events…</div>
        )}
        {transcript.entries.map((entry) => (
          <TranscriptEntryView
            key={entry.id}
            entry={entry}
            autoYes={autoYes}
            answeredRequests={answeredRequests}
            answerLabels={answerLabels}
            onRespondPermission={respondPermission}
            onRespondUserInput={respondUserInput}
          />
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

function TranscriptEntryView({
  entry,
  autoYes,
  answeredRequests,
  answerLabels,
  onRespondPermission,
  onRespondUserInput,
}: {
  entry: TranscriptEntry;
  autoYes: boolean;
  answeredRequests: Set<string>;
  answerLabels: Map<string, string>;
  onRespondPermission: (requestId: string, decision: 'approve' | 'reject') => Promise<void>;
  onRespondUserInput: (requestId: string, answer: string, wasFreeform: boolean) => Promise<void>;
}): JSX.Element {
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
        <PermissionRequestEntry
          entry={entry}
          autoYes={autoYes}
          answered={entry.requestId ? answeredRequests.has(entry.requestId) : false}
          answerLabel={entry.requestId ? answerLabels.get(entry.requestId) : undefined}
          onRespond={onRespondPermission}
        />
      );
    case 'input':
      return (
        <UserInputRequestEntry
          entry={entry}
          answered={entry.requestId ? answeredRequests.has(entry.requestId) : false}
          answerLabel={entry.requestId ? answerLabels.get(entry.requestId) : undefined}
          onRespond={onRespondUserInput}
        />
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

function PermissionRequestEntry({
  entry,
  autoYes,
  answered,
  answerLabel,
  onRespond,
}: {
  entry: Extract<TranscriptEntry, { kind: 'permission' }>;
  autoYes: boolean;
  answered: boolean;
  answerLabel?: string;
  onRespond: (requestId: string, decision: 'approve' | 'reject') => Promise<void>;
}): JSX.Element {
  const disabled = answered || !entry.requestId;

  return (
    <div className="transcript-entry transcript-entry--permission">
      <strong>Permission requested</strong>
      <span>{entry.question}</span>
      {entry.choices.length > 0 && <span className="transcript-entry__meta">{entry.choices.join(' / ')}</span>}
      {autoYes ? (
        <span className="transcript-entry__meta">AutoYes is enabled; permission will be handled by the daemon.</span>
      ) : (
        <div className="transcript-request__actions">
          <button
            type="button"
            className="transcript-request__button"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, 'approve')}
          >
            Approve
          </button>
          <button
            type="button"
            className="transcript-request__button transcript-request__button--secondary"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, 'reject')}
          >
            Reject
          </button>
          {answered && answerLabel && <span className="transcript-request__state">{answerLabel}</span>}
          {!entry.requestId && <span className="transcript-entry__meta">Missing request id.</span>}
        </div>
      )}
    </div>
  );
}

function UserInputRequestEntry({
  entry,
  answered,
  answerLabel,
  onRespond,
}: {
  entry: Extract<TranscriptEntry, { kind: 'input' }>;
  answered: boolean;
  answerLabel?: string;
  onRespond: (requestId: string, answer: string, wasFreeform: boolean) => Promise<void>;
}): JSX.Element {
  const [freeformText, setFreeformText] = useState('');
  const disabled = answered || !entry.requestId;
  const canSendFreeform = freeformText.trim().length > 0 && !disabled;

  const submitFreeform = (event: FormEvent): void => {
    event.preventDefault();
    const answer = freeformText.trim();
    if (!entry.requestId || !answer || answered) return;
    setFreeformText('');
    void onRespond(entry.requestId, answer, true);
  };

  return (
    <div className="transcript-entry transcript-entry--permission">
      <strong>Input requested</strong>
      <span>{entry.question}</span>
      <div className="transcript-request__actions">
        {entry.choices.map((choice) => (
          <button
            type="button"
            key={choice}
            className="transcript-request__button"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, choice, false)}
          >
            {choice}
          </button>
        ))}
        {answered && answerLabel && <span className="transcript-request__state">{answerLabel}</span>}
        {!entry.requestId && <span className="transcript-entry__meta">Missing request id.</span>}
      </div>
      <form className="transcript-request__freeform" onSubmit={submitFreeform}>
        <input
          type="text"
          value={freeformText}
          placeholder="Type a response…"
          disabled={disabled}
          onChange={(event) => setFreeformText(event.target.value)}
        />
        <button type="submit" className="transcript-request__button" disabled={!canSendFreeform}>
          Send
        </button>
      </form>
    </div>
  );
}
