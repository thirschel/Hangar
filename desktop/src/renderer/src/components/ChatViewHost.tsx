import type { FormEvent, JSX } from 'react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { EventFrame, WorkspaceInfo } from '../../../main/host-client';
import { Markdown } from './Markdown';
import { Composer } from './Composer';
import { ReviewPanel } from './ReviewPanel';
import { FilesPanel } from './FilesPanel';

// ChatViewHost is the rich chat surface for the new Agent mode: a top bar (chat
// title + AutoYes), a section nav (Chat / Changes / All files are live; MCP
// servers and Skills land later) and the streaming transcript with a composer
// slot. The Changes and All files pages reuse the standard ReviewPanel /
// FilesPanel embedded in the chat body. It subscribes to the same rich event
// stream as TranscriptView but presents it as a chat -- user turns as
// right-aligned bubbles, assistant turns as full-width Markdown-rendered text.
//
// The streaming pipeline below (frame reducer + permission / user-input handling)
// is a deliberate, isolated copy of TranscriptView's so the two rich surfaces can
// diverge without coupling; TranscriptView stays the standard-mode rich view.
export type ChatViewHostProps = {
  workspace: WorkspaceInfo;
};

// --- Section navigation ----------------------------------------------------
type ChatPage = 'chat' | 'mcp' | 'skills' | 'changes' | 'files';

type ChatNavTab = {
  id: ChatPage;
  label: string;
  enabled: boolean;
};

// Chat, Changes and All files are wired; MCP servers and Skills render as
// disabled "Coming soon" tabs and get their pages in a later task.
const NAV_TABS: ChatNavTab[] = [
  { id: 'chat', label: 'Chat', enabled: true },
  { id: 'mcp', label: 'MCP servers', enabled: false },
  { id: 'skills', label: 'Skills', enabled: false },
  { id: 'changes', label: 'Changes', enabled: true },
  { id: 'files', label: 'All files', enabled: true },
];

// --- Transcript model (isolated copy of TranscriptView's reducer) ----------
// The backend rich stream never emits user turns (see proto.go EventKind*: only
// assistant / tool / permission / usage / ... frames exist), so ChatView injects
// an optimistic, client-side frame for each sent message to show the user bubble.
const USER_LOCAL_KIND = 'user.local';
const SCROLL_SLOP = 48;

type TranscriptEntry =
  | { id: string; kind: 'user'; text: string }
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
  | { id: string; kind: 'permission'; requestId?: string; question: string; toolName?: string; choices: string[] }
  | { id: string; kind: 'input'; requestId?: string; question: string; choices: string[] }
  | { id: string; kind: 'usage'; text: string }
  | { id: string; kind: 'idle'; aborted: boolean }
  | { id: string; kind: 'error'; text: string };

type TranscriptModel = {
  entries: TranscriptEntry[];
  servers: Map<string, { status: string; error?: string }>;
  turnInProgress: boolean;
};

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

function mcpStatusMeta(status: string): { label: string; className: string } {
  return MCP_STATUS_META[status] ?? { label: status || 'Unknown', className: 'muted' };
}

function requestDetail(question: string, genericLabel: string, toolName?: string): string {
  const trimmedQuestion = question.trim();
  const nonGenericQuestion =
    trimmedQuestion.toLocaleLowerCase() === genericLabel.toLocaleLowerCase() ? '' : trimmedQuestion;
  return [toolName?.trim(), nonGenericQuestion].filter(Boolean).join(': ');
}

function buildTranscript(frames: EventFrame[]): TranscriptModel {
  const entries: TranscriptEntry[] = [];
  const servers = new Map<string, { status: string; error?: string }>();
  const toolEntries = new Map<string, number>();
  let pendingAssistantIndex: number | null = null;
  let pendingAssistantText = '';
  let turnInProgress = false;

  for (const frame of frames) {
    switch (frame.kind) {
      case USER_LOCAL_KIND:
        entries.push({ id: `user-${frame.seq}`, kind: 'user', text: frame.text ?? '' });
        break;
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
          question: frame.question ?? frame.text ?? '',
          toolName: frame.toolName,
          choices: frame.choices ?? [],
        });
        break;
      case 'user_input.requested':
        turnInProgress = true;
        entries.push({
          id: `input-${frame.requestId ?? frame.seq}`,
          kind: 'input',
          requestId: frame.requestId,
          question: frame.question ?? frame.text ?? '',
          choices: frame.choices ?? [],
        });
        break;
      case 'usage':
        entries.push({ id: `usage-${frame.seq}`, kind: 'usage', text: frameText(frame) });
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

  return { entries, servers, turnInProgress };
}

export function ChatViewHost({ workspace }: ChatViewHostProps): JSX.Element {
  const [framesBySeq, setFramesBySeq] = useState<Map<number, EventFrame>>(() => new Map());
  // Optimistic, client-side user-message frames (the backend never emits them).
  const [localUserFrames, setLocalUserFrames] = useState<EventFrame[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [optimisticTurn, setOptimisticTurn] = useState(false);
  const [answeredRequests, setAnsweredRequests] = useState<Set<string>>(() => new Set());
  const [answerLabels, setAnswerLabels] = useState<Map<string, string>>(() => new Map());
  const [autoYes, setAutoYes] = useState(workspace.autoYes);
  const [activePage, setActivePage] = useState<ChatPage>('chat');

  const scrollRef = useRef<HTMLDivElement>(null);
  const shouldStickToBottom = useRef(true);
  const localCounter = useRef(0);

  // Re-seed AutoYes from the workspace whenever the selected chat changes or the
  // server-truth value flips (App refresh). A local toggle leaves both unchanged,
  // so optimistic state survives until the daemon confirms it. This is React's
  // documented "adjust state during render" pattern -- no effect required.
  const [autoYesSeed, setAutoYesSeed] = useState(`${workspace.id}:${workspace.autoYes}`);
  const autoYesSeedNext = `${workspace.id}:${workspace.autoYes}`;
  if (autoYesSeed !== autoYesSeedNext) {
    setAutoYesSeed(autoYesSeedNext);
    setAutoYes(workspace.autoYes);
  }

  const frames = useMemo(() => {
    const merged = [...framesBySeq.values(), ...localUserFrames];
    return merged.sort((a, b) => a.seq - b.seq);
  }, [framesBySeq, localUserFrames]);
  const transcript = useMemo(() => buildTranscript(frames), [frames]);
  const turnInProgress = transcript.turnInProgress || optimisticTurn;

  // Subscribe to the rich event stream for this chat; re-subscribe when the chat
  // (session) changes and tear down on unmount. Mirrors TranscriptView.
  useEffect(() => {
    setFramesBySeq(new Map());
    setLocalUserFrames([]);
    setStreamError(null);
    setOptimisticTurn(false);
    setAnsweredRequests(new Set());
    setAnswerLabels(new Map());
    localCounter.current = 0;
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
      if (session === workspace.sessionName) addFrame(frame);
    });
    const unsubscribeError = window.cs.onRichError(({ session, message }) => {
      if (session === workspace.sessionName) setStreamError(message);
    });
    void window.cs.openRichStream(workspace.sessionName, 0).catch((error: unknown) => {
      setStreamError(error instanceof Error ? error.message : String(error));
    });

    return () => {
      unsubscribeFrame();
      unsubscribeError();
      void window.cs.closeRichStream(workspace.sessionName);
    };
  }, [workspace.sessionName]);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && shouldStickToBottom.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [frames.length, streamError, activePage]);

  const onScroll = (): void => {
    const el = scrollRef.current;
    if (!el) return;
    shouldStickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < SCROLL_SLOP;
  };

  const toggleAutoYes = (next: boolean): void => {
    setAutoYes(next); // optimistic; App refresh reconciles via autoYesSeed above
    void window.cs.setWorkspaceAutoYes(workspace.id, next).catch((error: unknown) => {
      setAutoYes(workspace.autoYes); // revert on failure
      setStreamError(error instanceof Error ? error.message : String(error));
    });
  };

  // Optimistic, client-side user frame: sort it just after the latest real frame
  // so it lands above the assistant's reply, with a per-message epsilon so
  // back-to-back sends stay uniquely ordered without colliding with integer seqs.
  const nextLocalSeq = (): number => {
    let realMax = 0;
    for (const seq of framesBySeq.keys()) {
      if (seq > realMax) realMax = seq;
    }
    localCounter.current += 1;
    return realMax + 0.5 + localCounter.current * 1e-6;
  };

  // --- Composer slot contract (consumed by <Composer/>; see slot below) -----
  const handleSend = (text: string): void => {
    const message = text.trim();
    if (!message) return;
    const seq = nextLocalSeq();
    setLocalUserFrames((current) => [...current, { seq, kind: USER_LOCAL_KIND, text: message }]);
    setOptimisticTurn(true);
    void window.cs.sendMessage(workspace.sessionName, message).catch((error: unknown) => {
      setOptimisticTurn(false);
      setStreamError(error instanceof Error ? error.message : String(error));
    });
  };

  const handleStop = (): void => {
    setOptimisticTurn(false);
    void window.cs.abortTurn(workspace.sessionName).catch((error: unknown) => {
      setStreamError(error instanceof Error ? error.message : String(error));
    });
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
      await window.cs.respondPermission(workspace.sessionName, requestId, decision);
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
      await window.cs.respondUserInput(workspace.sessionName, requestId, answer, wasFreeform);
    } catch (error) {
      unmarkAnswered(requestId);
      setStreamError(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <section className="chat-view" aria-label="Chat conversation">
      <header className="chat-view__topbar">
        <h2 className="chat-view__title">{workspace.title}</h2>
        <label className="autoyes" title="Auto-approve agent prompts (host-side)">
          <input
            type="checkbox"
            checked={autoYes}
            onChange={(event) => toggleAutoYes(event.target.checked)}
          />
          AutoYes
        </label>
      </header>

      <nav className="chat-view__nav" aria-label="Chat sections">
        {NAV_TABS.map((tab) => {
          const active = activePage === tab.id;
          return (
            <button
              key={tab.id}
              type="button"
              className={`chat-view__tab${active ? ' chat-view__tab--active' : ''}`}
              disabled={!tab.enabled}
              aria-current={active ? 'page' : undefined}
              title={tab.enabled ? undefined : 'Coming soon'}
              onClick={() => setActivePage(tab.id)}
            >
              {tab.label}
            </button>
          );
        })}
      </nav>

      {activePage === 'chat' && (
        <div className="chat-view__body">
          {transcript.servers.size > 0 && (
            <div className="chat-view__mcp" aria-label="MCP server status">
              <span className="chat-view__mcp-title">MCP</span>
              <div className="chat-view__mcp-list">
                {Array.from(transcript.servers.entries()).map(([server, info]) => {
                  const meta = mcpStatusMeta(info.status);
                  return (
                    <span key={server} className="chat-view__mcp-pill" title={info.error}>
                      <span className="chat-view__mcp-server">{server}</span>
                      <span
                        className={`chat-view__mcp-status chat-view__mcp-status--${meta.className}`}
                      >
                        {meta.label}
                      </span>
                    </span>
                  );
                })}
              </div>
            </div>
          )}

          <div ref={scrollRef} className="chat-view__scroll" onScroll={onScroll}>
            {transcript.entries.length === 0 && !streamError && (
              <div className="chat-view__empty">Waiting for the agent…</div>
            )}
            {transcript.entries.map((entry) => (
              <ChatEntryView
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
              <div className="chat-entry chat-entry--error" role="alert">
                {streamError}
              </div>
            )}
          </div>

          <div className="chat-view__composer-slot">
            <Composer
              turnInProgress={turnInProgress}
              onSend={handleSend}
              onStop={handleStop}
              info={<>model · context%</>}
            />
          </div>
        </div>
      )}

      {activePage === 'changes' && (
        <div className="chat-view__body chat-view__page">
          <ReviewPanel workspace={workspace} embedded active={activePage === 'changes'} />
        </div>
      )}

      {activePage === 'files' && (
        <div className="chat-view__body chat-view__page">
          <FilesPanel workspace={workspace} embedded />
        </div>
      )}

      {(activePage === 'mcp' || activePage === 'skills') && (
        <div className="chat-view__body chat-view__stub">
          <p className="chat-view__stub-text">Coming soon</p>
        </div>
      )}
    </section>
  );
}

function ChatEntryView({
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
    case 'user':
      return (
        <div className="chat-msg chat-msg--user">
          <div className="chat-msg__bubble">{entry.text}</div>
        </div>
      );
    case 'assistant':
      return (
        <div className="chat-msg chat-msg--assistant">
          <Markdown text={entry.text} />
          {entry.streaming && <span className="chat-msg__cursor" aria-label="streaming" />}
        </div>
      );
    case 'reasoning':
      return (
        <details className="chat-entry chat-entry--reasoning">
          <summary>Reasoning</summary>
          <div className="chat-entry__text">{entry.text}</div>
        </details>
      );
    case 'tool':
      return (
        <div className={`chat-tool chat-tool--${entry.status}`}>
          <span className="chat-tool__state">{entry.status === 'running' ? 'Running' : 'Done'}</span>
          <span className="chat-tool__name">{entry.toolName}</span>
          {entry.mcpServer && <span className="chat-tool__server">{entry.mcpServer}</span>}
          {entry.detail && <span className="chat-tool__detail">{entry.detail}</span>}
        </div>
      );
    case 'permission':
      return (
        <ChatPermissionEntry
          entry={entry}
          autoYes={autoYes}
          answered={entry.requestId ? answeredRequests.has(entry.requestId) : false}
          answerLabel={entry.requestId ? answerLabels.get(entry.requestId) : undefined}
          onRespond={onRespondPermission}
        />
      );
    case 'input':
      return (
        <ChatUserInputEntry
          entry={entry}
          answered={entry.requestId ? answeredRequests.has(entry.requestId) : false}
          answerLabel={entry.requestId ? answerLabels.get(entry.requestId) : undefined}
          onRespond={onRespondUserInput}
        />
      );
    case 'usage':
      return <div className="chat-entry chat-entry--usage">{entry.text || 'Usage updated'}</div>;
    case 'idle':
      return (
        <div className="chat-entry chat-entry--usage">
          {entry.aborted ? 'Turn aborted.' : 'Turn complete.'}
        </div>
      );
    case 'error':
      return (
        <div className="chat-entry chat-entry--error" role="alert">
          {entry.text}
        </div>
      );
  }
}

function ChatPermissionEntry({
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
  const detail = requestDetail(entry.question, 'Permission requested', entry.toolName);

  return (
    <div className="chat-entry chat-entry--permission">
      <strong>Permission requested</strong>
      {detail && <span>{detail}</span>}
      {entry.choices.length > 0 && <span className="chat-entry__meta">{entry.choices.join(' / ')}</span>}
      {autoYes ? (
        <span className="chat-entry__meta">
          AutoYes is enabled; permission will be handled by the daemon.
        </span>
      ) : (
        <div className="chat-request__actions">
          <button
            type="button"
            className="chat-request__button"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, 'approve')}
          >
            Approve
          </button>
          <button
            type="button"
            className="chat-request__button chat-request__button--secondary"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, 'reject')}
          >
            Reject
          </button>
          {answered && answerLabel && <span className="chat-request__state">{answerLabel}</span>}
          {!entry.requestId && <span className="chat-entry__meta">Missing request id.</span>}
        </div>
      )}
    </div>
  );
}

function ChatUserInputEntry({
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
  const detail = requestDetail(entry.question, 'Input requested');

  const submitFreeform = (event: FormEvent): void => {
    event.preventDefault();
    const answer = freeformText.trim();
    if (!entry.requestId || !answer || answered) return;
    setFreeformText('');
    void onRespond(entry.requestId, answer, true);
  };

  return (
    <div className="chat-entry chat-entry--permission">
      <strong>Input requested</strong>
      {detail && <span>{detail}</span>}
      <div className="chat-request__actions">
        {entry.choices.map((choice) => (
          <button
            type="button"
            key={choice}
            className="chat-request__button"
            disabled={disabled}
            onClick={() => entry.requestId && void onRespond(entry.requestId, choice, false)}
          >
            {choice}
          </button>
        ))}
        {answered && answerLabel && <span className="chat-request__state">{answerLabel}</span>}
        {!entry.requestId && <span className="chat-entry__meta">Missing request id.</span>}
      </div>
      <form className="chat-request__freeform" onSubmit={submitFreeform}>
        <input
          type="text"
          value={freeformText}
          placeholder="Type a response…"
          disabled={disabled}
          onChange={(event) => setFreeformText(event.target.value)}
        />
        <button type="submit" className="chat-request__button" disabled={!canSendFreeform}>
          Send
        </button>
      </form>
    </div>
  );
}