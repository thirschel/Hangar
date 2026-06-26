import type { FormEvent, JSX } from 'react';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type {
  AgentInfo,
  EventFrame,
  InstructionInfo,
  McpServerInfo,
  ModelInfo,
  SkillInfo,
  WorkspaceInfo,
} from '../../../main/host-client';
import { Markdown } from './Markdown';
import { Composer } from './Composer';
import { ChatSearchBar } from './ChatSearchBar';
import { useTranscriptSearch } from './useTranscriptSearch';
import { ReviewPanel } from './ReviewPanel';
import { FilesPanel } from './FilesPanel';
import { McpPage } from './McpPage';
import { SkillsPage } from './SkillsPage';
import { InstructionsPage } from './InstructionsPage';
import { AgentsPage } from './AgentsPage';

// ChatViewHost is the rich chat surface for the new Agent mode: a top bar (chat
// title + AutoYes), a section nav (Chat / MCP servers / Skills / Changes / All
// files) and the streaming transcript with a composer slot. The Changes and All
// files pages reuse the standard ReviewPanel / FilesPanel embedded in the chat
// body; the MCP servers and Skills pages render the latest structured snapshots
// (mcp.detail / skills frames) carried on the same rich event stream. It
// subscribes to that stream and presents it as a chat -- user turns as
// right-aligned bubbles, assistant turns as full-width Markdown-rendered text.
//
// The streaming pipeline below (frame reducer + permission / user-input handling)
// is a deliberate, isolated copy of TranscriptView's so the two rich surfaces can
// diverge without coupling; TranscriptView stays the standard-mode rich view.
export type ChatViewHostProps = {
  workspace: WorkspaceInfo;
  // Which instance answers the Ctrl/Cmd+F find hotkey. 'global' (default) always
  // responds — used for the single full-screen agent view. 'active' responds only
  // when this chat is hovered or focus-within, so in the grid the hotkey targets
  // the tile the user is on rather than every tile at once.
  findHotkeyScope?: 'global' | 'active';
};

// --- Section navigation ----------------------------------------------------
type ChatPage = 'chat' | 'mcp' | 'skills' | 'instructions' | 'agents' | 'changes' | 'files';

type ChatNavTab = {
  id: ChatPage;
  label: string;
  enabled: boolean;
};

// All five sections are wired: Chat / Changes / All files embed inline panels,
// while MCP servers and Skills render full-middle snapshot pages fed by the rich
// event stream.
const NAV_TABS: ChatNavTab[] = [
  { id: 'chat', label: 'Chat', enabled: true },
  { id: 'mcp', label: 'MCP servers', enabled: true },
  { id: 'skills', label: 'Skills', enabled: true },
  { id: 'instructions', label: 'Instructions', enabled: true },
  { id: 'agents', label: 'Agents', enabled: true },
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
  | { id: string; kind: 'assistant'; text: string; streaming: boolean; fromDelta?: boolean }
  | { id: string; kind: 'reasoning'; text: string; streaming?: boolean; fromDelta?: boolean }
  | {
      id: string;
      kind: 'tool';
      toolName: string;
      mcpServer?: string;
      toolCallId?: string;
      status: 'running' | 'done' | 'error';
      permission?: { toolName?: string; question: string; requestId?: string };
      // Concise CLI-style detail: args captured on tool.start, result merged in
      // on tool.complete (the daemon puts the error message in result on failure).
      args?: string;
      result?: string;
    }
  | {
      id: string;
      kind: 'permission';
      requestId?: string;
      toolCallId?: string;
      question: string;
      toolName?: string;
      choices: string[];
      linkedToTool?: boolean;
      // Answered-state derived in the reducer from a replayed 'permission.resolved'
      // frame, so it survives a fresh mount even when the optimistic local
      // answeredRequests set is empty. answerLabel is "approved" / "rejected".
      answered?: boolean;
      answerLabel?: string;
    }
  | {
      id: string;
      kind: 'input';
      requestId?: string;
      question: string;
      choices: string[];
      // Answered-state derived from a replayed 'input.resolved' frame (label
      // "answered"); mirrors the permission entry above so it survives a remount.
      answered?: boolean;
      answerLabel?: string;
    }
  | { id: string; kind: 'idle'; aborted: boolean; timestamp?: number }
  | { id: string; kind: 'error'; text: string };

type TranscriptModel = {
  entries: TranscriptEntry[];
  servers: Map<string, { status: string; error?: string }>;
  // Latest full-list snapshots from the rich stream (last-write-wins): a
  // 'mcp.detail' frame replaces mcpServers, a 'skills' frame replaces skills.
  // These feed the dedicated MCP servers / Skills pages and are independent of
  // the pill bar (`servers`), which keeps tracking inline 'mcp.status' updates.
  mcpServers: McpServerInfo[];
  skills: SkillInfo[];
  // Latest 'instructions' snapshot (last-write-wins): the custom instructions the
  // SDK loaded for the session, feeding the dedicated Instructions page.
  instructions: InstructionInfo[];
  // Latest 'agents' snapshot (last-write-wins): the custom agents discovered for the
  // session, feeding the dedicated Agents page.
  agents: AgentInfo[];
  turnInProgress: boolean;
  // Latest context-usage snapshot from a 'usage' frame (last-write-wins). Drives
  // the composer header (active model + context %). Undefined until the first
  // usage frame arrives.
  usage?: UsageSnapshot;
  // Latest model-selection snapshot from a 'model' frame (last-write-wins). The
  // daemon emits it on start/resume and on a live switch, so it restores the
  // selector (model + effort + context tier) after a remount/replay. Undefined
  // until the first model frame arrives.
  modelState?: ModelSelectionSnapshot;
};

// A single context-usage reading carried on a 'usage' frame. Context % is
// currentTokens / tokenLimit; any field may be absent (guard before dividing).
type UsageSnapshot = {
  model?: string;
  currentTokens?: number;
  tokenLimit?: number;
  aic?: number;
};

// The session's current model selection carried on a 'model' frame: the model id
// plus the optional reasoning effort and context tier. Last-write-wins across the
// seq-sorted frames; used to restore the selector after a resume/remount.
type ModelSelectionSnapshot = {
  model?: string;
  effort?: string;
  contextTier?: string;
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

// Basename of an absolute path (handles both Windows and POSIX separators),
// used to label attachments in the optimistic user bubble.
function basenameOf(filePath: string): string {
  const segments = filePath.split(/[\\/]/);
  return segments[segments.length - 1] || filePath;
}

// Build the optimistic user-bubble text. When files are attached, append a
// "📎 name, name" summary (basenames) so the user sees what they sent; this is
// display-only -- the message text and the attachment paths reach the daemon
// separately via sendMessage(session, message, attachments).
function composeUserBubble(message: string, attachments: string[]): string {
  if (attachments.length === 0) return message;
  const summary = `\uD83D\uDCCE ${attachments.map(basenameOf).join(', ')}`;
  return message ? `${message}\n\n${summary}` : summary;
}

function toolKey(frame: EventFrame): string {
  return frame.toolCallId ?? frame.requestId ?? `${frame.toolName ?? 'tool'}:${frame.mcpServer ?? ''}`;
}

function mcpStatusMeta(status: string): { label: string; className: string } {
  return MCP_STATUS_META[status] ?? { label: status || 'Unknown', className: 'muted' };
}

// Shallow id/name equality for two model lists. Lets the ListModels fetch skip a
// no-op state update (e.g. an empty list resolving over an already-empty one),
// which keeps the selector from re-rendering on every chat mount.
function sameModelList(a: ModelInfo[], b: ModelInfo[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((model, i) => model.id === b[i].id && model.name === b[i].name);
}

function requestDetail(question: string, genericLabel: string, toolName?: string): string {
  const trimmedQuestion = question.trim();
  const nonGenericQuestion =
    trimmedQuestion.toLocaleLowerCase() === genericLabel.toLocaleLowerCase() ? '' : trimmedQuestion;
  return [toolName?.trim(), nonGenericQuestion].filter(Boolean).join(': ');
}

// --- Activity status (CLI-style "Working" / "Searching…" line) --------------
// The SDK's AssistantIntentData proved unreliable (it did not fire for a real
// multi-step task), so the status is derived from the live transcript instead --
// model-independent and grounded in events we already render.
const TOOL_STATUS_VERBS: ReadonlyArray<readonly [RegExp, string]> = [
  [/grep|search|find|lookup/, 'Searching'],
  [/view|read|cat|open|list|ls/, 'Reading'],
  [/bash|shell|run|exec|terminal|powershell|cmd|command/, 'Running'],
  [/write|edit|create|replace|apply|patch|insert|delete/, 'Editing'],
  [/fetch|web|url|http|browse|download|curl/, 'Fetching'],
];

function toolStatusVerb(toolName: string): string {
  const name = toolName.toLowerCase();
  for (const [pattern, verb] of TOOL_STATUS_VERBS) {
    if (pattern.test(name)) return verb;
  }
  return `Using ${toolName}`;
}

// Derive a short status label for the most recent activity in the current turn.
// Callers gate on turnInProgress, so this only runs while the agent is busy.
function deriveActivityStatus(entries: TranscriptEntry[]): string {
  // A currently-running tool is the most specific "what is it doing now"; scan
  // back only within the latest turn segment (stop at the previous idle marker).
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i];
    if (entry.kind === 'idle') break;
    if (entry.kind === 'tool' && entry.status === 'running') {
      return toolStatusVerb(entry.toolName);
    }
  }
  const last = entries[entries.length - 1];
  if (last?.kind === 'reasoning') return 'Thinking';
  if (last?.kind === 'assistant' && last.streaming) return 'Responding';
  return 'Working';
}

function buildTranscript(frames: EventFrame[]): TranscriptModel {
  const entries: TranscriptEntry[] = [];
  const servers = new Map<string, { status: string; error?: string }>();
  const toolEntries = new Map<string, number>();
  const pendingPermissionByToolCallId = new Map<string, number>();
  let pendingAssistantIndex: number | null = null;
  let pendingAssistantText = '';
  // Seq of the assistant turn's first delta. Reused as the entry id across both
  // the streaming and finalized states so completion reconciles the entry in
  // place instead of remounting it (a remount would re-flash the word fade-in).
  let pendingAssistantSeq: number | null = null;
  // Mirror of the assistant accumulator for reasoning: deltas grow a single
  // pending reasoning entry, finalized by the full 'assistant.reasoning' frame.
  let pendingReasoningIndex: number | null = null;
  let pendingReasoningText = '';
  let pendingReasoningSeq: number | null = null;
  let turnInProgress = false;
  // Full-list snapshots; the last matching frame wins (frames are seq-sorted).
  let mcpServers: McpServerInfo[] = [];
  let skills: SkillInfo[] = [];
  let instructions: InstructionInfo[] = [];
  let agents: AgentInfo[] = [];
  // Latest usage reading; last-write-wins across the seq-sorted frames.
  let usage: UsageSnapshot | undefined;
  // Latest model selection; last-write-wins across the seq-sorted frames.
  let modelState: ModelSelectionSnapshot | undefined;
  // requestId -> answered label, derived from 'permission.resolved' /
  // 'input.resolved' frames so answered-state survives a fresh mount/replay
  // (independent of the optimistic local answeredRequests marks).
  const resolved = new Map<string, string>();

  for (const frame of frames) {
    switch (frame.kind) {
      case USER_LOCAL_KIND:
        entries.push({ id: `user-${frame.seq}`, kind: 'user', text: frame.text ?? '' });
        break;
      case 'assistant.delta': {
        pendingAssistantText += frame.text ?? '';
        turnInProgress = true;
        if (pendingAssistantIndex === null) {
          // Capture the first-delta seq and reuse it for the entry id through the
          // streaming and finalized states (see pendingAssistantSeq above).
          pendingAssistantSeq = frame.seq;
          pendingAssistantIndex = entries.length;
          entries.push({
            id: `assistant-${pendingAssistantSeq}`,
            kind: 'assistant',
            text: pendingAssistantText,
            streaming: true,
            // Built from a delta => generated live in front of the user, so the
            // typewriter reveal animates it. Replayed history arrives as a direct
            // 'assistant.message' (no deltas) and stays fromDelta:false => instant.
            fromDelta: true,
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
            // Keep the streaming entry's stable id (first-delta seq) so the
            // streaming -> finalized transition reconciles in place rather than
            // remounting and re-animating the whole message.
            id: `assistant-${pendingAssistantSeq ?? frame.seq}`,
            kind: 'assistant',
            text,
            streaming: false,
            // This turn streamed deltas => keep it animating to completion via the
            // reveal even though it just finalized (short replies finalize within
            // ~6ms of their single delta burst, so the reveal -- not `streaming` --
            // is what makes them write out).
            fromDelta: true,
          };
        } else {
          // No deltas preceded this full message => replayed history / instant.
          entries.push({
            id: `assistant-${frame.seq}`,
            kind: 'assistant',
            text,
            streaming: false,
            fromDelta: false,
          });
        }
        pendingAssistantIndex = null;
        pendingAssistantText = '';
        pendingAssistantSeq = null;
        turnInProgress = false;
        break;
      }
      case 'assistant.reasoning.delta': {
        turnInProgress = true;
        pendingReasoningText += frame.text ?? '';
        if (pendingReasoningIndex === null) {
          // First reasoning delta: create the pending entry (marked streaming so
          // it can be finalized below); reuse this seq as a stable id.
          pendingReasoningSeq = frame.seq;
          pendingReasoningIndex = entries.length;
          entries.push({
            id: `reasoning-${pendingReasoningSeq}`,
            kind: 'reasoning',
            text: pendingReasoningText,
            streaming: true,
            fromDelta: true,
          });
        } else {
          entries[pendingReasoningIndex] = {
            ...entries[pendingReasoningIndex],
            kind: 'reasoning',
            text: pendingReasoningText,
            streaming: true,
          } as TranscriptEntry;
        }
        break;
      }
      case 'assistant.reasoning': {
        turnInProgress = true;
        const text = frameText(frame);
        if (pendingReasoningIndex !== null) {
          // Finalize the streamed reasoning: replace the accumulated text with the
          // authoritative complete block and clear the streaming flag (same id).
          entries[pendingReasoningIndex] = {
            id: `reasoning-${pendingReasoningSeq ?? frame.seq}`,
            kind: 'reasoning',
            text,
            fromDelta: true,
          };
        } else {
          // No deltas were streamed: push a finished reasoning entry as before
          // (replayed history / a full-only reasoning => rendered instantly).
          entries.push({ id: `reasoning-${frame.seq}`, kind: 'reasoning', text, fromDelta: false });
        }
        pendingReasoningIndex = null;
        pendingReasoningText = '';
        pendingReasoningSeq = null;
        break;
      }
      case 'tool.start': {
        turnInProgress = true;
        const entry: TranscriptEntry = {
          id: `tool-${toolKey(frame)}-${frame.seq}`,
          kind: 'tool',
          toolName: frame.toolName ?? 'Tool',
          mcpServer: frame.mcpServer,
          toolCallId: frame.toolCallId,
          status: 'running',
          args: frame.toolArgs,
        };
        if (frame.toolCallId) {
          const permissionIndex = pendingPermissionByToolCallId.get(frame.toolCallId);
          const permissionEntry = permissionIndex !== undefined ? entries[permissionIndex] : undefined;
          if (permissionIndex !== undefined && permissionEntry?.kind === 'permission') {
            entry.permission = {
              toolName: permissionEntry.toolName,
              question: permissionEntry.question,
              requestId: permissionEntry.requestId,
            };
            entries[permissionIndex] = { ...permissionEntry, linkedToTool: true };
          }
        }
        toolEntries.set(toolKey(frame), entries.length);
        entries.push(entry);
        break;
      }
      case 'tool.complete': {
        const key = toolKey(frame);
        const index = toolEntries.get(key);
        // Merge onto the matching tool.start: keep the start's args, attach the
        // concise result (the daemon also puts the error message in toolResult),
        // and flip the status to done/error so the inline dot recolors.
        const prev = index !== undefined ? entries[index] : undefined;
        const next: Extract<TranscriptEntry, { kind: 'tool' }> = {
          id: prev?.id ?? `tool-${key}-${frame.seq}`,
          kind: 'tool',
          toolName: frame.toolName ?? (prev?.kind === 'tool' ? prev.toolName : undefined) ?? 'Tool',
          mcpServer: frame.mcpServer ?? (prev?.kind === 'tool' ? prev.mcpServer : undefined),
          toolCallId: prev?.kind === 'tool' ? prev.toolCallId : frame.toolCallId,
          status: frame.error ? 'error' : 'done',
          permission: prev?.kind === 'tool' ? prev.permission : undefined,
          args: prev?.kind === 'tool' ? prev.args : undefined,
          result: frame.toolResult ?? frame.error,
        };
        if (index === undefined) entries.push(next);
        else entries[index] = next;
        break;
      }
      case 'permission.requested': {
        turnInProgress = true;
        const entry: TranscriptEntry = {
          id: `permission-${frame.requestId ?? frame.seq}`,
          kind: 'permission',
          requestId: frame.requestId,
          toolCallId: frame.toolCallId,
          question: frame.question ?? frame.text ?? '',
          toolName: frame.toolName,
          choices: frame.choices ?? [],
        };
        if (frame.toolCallId) {
          const toolIndex = toolEntries.get(frame.toolCallId);
          const toolEntry = toolIndex !== undefined ? entries[toolIndex] : undefined;
          if (toolIndex !== undefined && toolEntry?.kind === 'tool') {
            // Tool-before-permission ordering (the SDK emits tool.start BEFORE the
            // permission.requested for reads/view): the tool entry already exists,
            // so link to it now. First permission wins the popover detail; later
            // permissions for the same call still hide their own bubble.
            if (!toolEntry.permission) {
              entries[toolIndex] = {
                ...toolEntry,
                permission: {
                  toolName: frame.toolName,
                  question: entry.question,
                  requestId: frame.requestId,
                },
              };
            }
            entry.linkedToTool = true;
          } else {
            // Permission-before-tool ordering (e.g. shell): buffer for tool.start.
            pendingPermissionByToolCallId.set(frame.toolCallId, entries.length);
          }
        }
        entries.push(entry);
        break;
      }
      case 'permission.resolved':
        // A permission was answered (approve/reject). Record the decision label so
        // the matching permission entry replays as answered after a remount; the
        // label mirrors the optimistic local mark (markAnswered) for consistency.
        if (frame.requestId) {
          resolved.set(frame.requestId, frame.decision === 'approve' ? 'approved' : 'rejected');
        }
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
      case 'input.resolved':
        // A user_input/elicitation was answered. Record the generic "answered"
        // label so the matching input entry replays as answered after a remount.
        if (frame.requestId) {
          resolved.set(frame.requestId, 'answered');
        }
        break;
      case 'usage':
        // Capture the structured reading for the composer header (active model +
        // context %). The CLI shows no inline "Usage updated" line, so we never
        // push a transcript entry -- only the header consumes this snapshot
        // (last-write-wins across the seq-sorted frames).
        usage = {
          model: frame.model,
          currentTokens: frame.currentTokens,
          tokenLimit: frame.tokenLimit,
          aic: frame.aic,
        };
        break;
      case 'model':
        // The session's current model selection (model + effort + context tier),
        // emitted on start/resume and on a live switch. Captured last-write-wins
        // (like usage) and used to restore the selector after a remount/replay.
        modelState = {
          model: frame.model,
          effort: frame.effort,
          contextTier: frame.contextTier,
        };
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
          pendingAssistantSeq = null;
        }
        // Finalize a still-streaming reasoning entry too, so a turn that ends
        // without a full 'assistant.reasoning' frame doesn't stay marked
        // streaming (clears the flag; same id, no remount).
        if (pendingReasoningIndex !== null) {
          entries[pendingReasoningIndex] = {
            ...entries[pendingReasoningIndex],
            kind: 'reasoning',
            streaming: false,
          } as TranscriptEntry;
          pendingReasoningIndex = null;
          pendingReasoningText = '';
          pendingReasoningSeq = null;
        }
        turnInProgress = false;
        entries.push({
          id: `idle-${frame.seq}`,
          kind: 'idle',
          aborted: frame.aborted ?? false,
          timestamp: frame.ts,
        });
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
      case 'mcp.detail':
        // Full MCP server list snapshot; replace wholesale (last-write-wins).
        if (frame.mcpServers) mcpServers = frame.mcpServers;
        break;
      case 'skills':
        // Full skills list snapshot; replace wholesale (last-write-wins).
        if (frame.skills) skills = frame.skills;
        break;
      case 'instructions':
        // Full custom-instructions snapshot; replace wholesale (last-write-wins).
        if (frame.instructions) instructions = frame.instructions;
        break;
      case 'agents':
        // Full custom-agents snapshot; replace wholesale (last-write-wins).
        if (frame.agents) agents = frame.agents;
        break;
      default:
        break;
    }
  }

  // Stamp answered-state onto permission/input entries from the resolved map so a
  // replayed *.requested + *.resolved pair renders answered (decision label, no
  // active buttons) even on a fresh mount where answeredRequests is still empty.
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    if (entry.kind !== 'permission' && entry.kind !== 'input') continue;
    if (!entry.requestId) continue;
    const label = resolved.get(entry.requestId);
    if (label) entries[i] = { ...entry, answered: true, answerLabel: label };
  }

  return { entries, servers, mcpServers, skills, instructions, agents, turnInProgress, usage, modelState };
}

// Context % = currentTokens / tokenLimit, rendered as e.g. "42% context used".
// Returns undefined when the reading is missing or would divide by zero, so the
// header shows nothing rather than "NaN%".
function formatContextPercent(usage?: UsageSnapshot): string | undefined {
  if (!usage) return undefined;
  const { currentTokens, tokenLimit } = usage;
  if (typeof currentTokens !== 'number' || typeof tokenLimit !== 'number' || tokenLimit <= 0) {
    return undefined;
  }
  return `${Math.round((currentTokens / tokenLimit) * 100)}% context used`;
}

function formatAic(units: number): string {
  return units.toFixed(1).replace(/\.0$/, '');
}

function formatCompletedTime(ms?: number): string {
  if (typeof ms === 'number' && Number.isFinite(ms)) {
    return `Completed ${new Date(ms).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}`;
  }
  return 'Turn complete.';
}

// Per-session rich-frame cache, kept at module scope so it survives both a
// ChatViewHost remount and a rich-stream re-subscribe (desktop.log shows the
// event stream closing and reopening every ~7-15s). Each entry is that
// session's seq->frame map. Seeding `framesBySeq` from this cache on every
// (re)subscribe keeps the transcript on screen instead of blanking it to empty
// while the daemon replays the since=0 snapshot -- the replayed frames merge
// idempotently by seq, so the transcript no longer "pops in" and live deltas
// keep fading word-by-word. The cache is intentionally never evicted: a chat
// transcript is small and the user can switch back to any prior session.
const richFrameCache = new Map<string, Map<number, EventFrame>>();

// Test-only hook: clear the module-global cache so frames never leak across
// cases. The ChatViewHost test suite calls this in its beforeEach.
// eslint-disable-next-line react-refresh/only-export-components
export function __clearRichFrameCacheForTests(): void {
  richFrameCache.clear();
}

// --- Typewriter reveal -----------------------------------------------------
// The Copilot SDK does NOT stream smooth tokens: a measured delta-timing spike
// showed it flushes deltas in coarse bursts (a whole short reply is a SINGLE
// burst that finalizes ~6ms later; a long reply arrives in ~8s bursts of 15-20
// words at the same instant). So animating off raw delta cadence can't produce a
// "writing" effect. Instead we reveal the (already-accumulated) target text on a
// timer, word-by-word, decoupled from how/when the bytes arrived -- exactly how
// Claude/ChatGPT replay buffered output at a smooth rate.
//
// Progress is kept in a module-level store keyed by entry id so it survives a
// ChatViewHost remount AND the ~7-15s rich-stream re-subscribe (the cache replays
// the whole transcript on every resubscribe, which would otherwise restart the
// reveal). Once an id is `done` it stays done; an in-flight reveal resumes from
// its stored position.
type RevealState = { revealed: number; done: boolean };
const revealStore = new Map<string, RevealState>();

// Test-only hook: clear the module-global reveal store so progress never leaks
// across cases (mirrors __clearRichFrameCacheForTests).
// eslint-disable-next-line react-refresh/only-export-components
export function __clearRevealStoreForTests(): void {
  revealStore.clear();
}

// ~40fps frame budget: fast enough to read as smooth writing, slow enough to keep
// ~31fps frame budget: a gentle, readable cadence that lets each word's fade
// register (rather than a burst settling at once), while keeping the per-frame
// Markdown re-parse cheap. Reveal speed is proportional to the backlog (so an 8s
// SDK burst still catches up in ~1.5s) but clamped so we never dump the whole
// message at once nor crawl one char at a time.
const REVEAL_FRAME_MS = 32;
const REVEAL_CATCHUP_DIVISOR = 28;
const REVEAL_MIN_CHARS = 2;
const REVEAL_MAX_CHARS = 40;

// Whether to animate at all. In the Electron renderer `matchMedia` always exists,
// so this is just the reduced-motion preference. In jsdom (vitest) `matchMedia`
// is absent => treat as "no animation" so component tests see fully-revealed text
// immediately; the dedicated reveal tests install a matchMedia stub to opt in.
function revealAnimationEnabled(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Advance a character count forward to the next whitespace boundary so we only
// ever reveal whole words (never cut a word mid-render). Terminates at end of text.
function snapToWordEnd(text: string, count: number): number {
  let n = Math.min(count, text.length);
  while (n < text.length && /\S/.test(text[n])) n += 1;
  return n;
}

// Drive the progressive reveal of `text` for the entry `id`. `streaming` defers
// the `done` mark so a mid-stream pause (caught up to the text we have so far)
// doesn't permanently stop the reveal. `eligible` is the reducer's `fromDelta`:
// true only for messages generated live (built from deltas); replayed history and
// full-only messages snap to full. Returns the visible slice + whether a reveal
// is still in progress (drives the trailing cursor and the per-word fade).
function useReveal(
  id: string,
  text: string,
  streaming: boolean,
  eligible: boolean,
): { shown: string; revealing: boolean } {
  const target = text.length;
  const [revealed, setRevealed] = useState<number>(() => {
    const existing = revealStore.get(id);
    if (existing) return Math.min(existing.revealed, target);
    // First time we see this id: animate only a live (delta-built) message when
    // motion is allowed; everything else is revealed in full at once.
    if (!eligible || !revealAnimationEnabled()) {
      revealStore.set(id, { revealed: target, done: true });
      return target;
    }
    revealStore.set(id, { revealed: 0, done: false });
    return 0;
  });

  // Latest text/target/streaming for the tick loop, which is started once per id
  // and must NOT restart on every render (and must keep ticking on its own rather
  // than rescheduling from a re-render -- a per-render reschedule stalls because
  // React passive effects don't reliably flush between fake-timer ticks).
  const textRef = useRef(text);
  textRef.current = text;
  const targetRef = useRef(target);
  targetRef.current = target;
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Advance loop: one interval per entry id. The updater is pure (no store writes
  // / no clears); persistence + finalize/stop live in the sync effect below.
  useEffect(() => {
    if (revealStore.get(id)?.done) return; // snapped to full: no loop needed
    const interval = setInterval(() => {
      setRevealed((current) => {
        const tgt = targetRef.current;
        if (current >= tgt) return current; // caught up; await more text
        const remaining = tgt - current;
        const chunk = Math.min(
          REVEAL_MAX_CHARS,
          Math.max(REVEAL_MIN_CHARS, Math.ceil(remaining / REVEAL_CATCHUP_DIVISOR)),
        );
        return snapToWordEnd(textRef.current, current + chunk);
      });
    }, REVEAL_FRAME_MS);
    intervalRef.current = interval;
    return () => {
      clearInterval(interval);
      intervalRef.current = null;
    };
  }, [id]);

  // Persist progress to the module store (survives remount / re-subscribe) and
  // finalize + stop the loop once we've caught up and the stream has ended. Also
  // keeps a snapped (done) entry pinned to full if its text grew after the snap.
  useEffect(() => {
    const state = revealStore.get(id);
    if (state?.done) {
      if (revealed < target) setRevealed(target);
      return;
    }
    if (revealed >= target && !streaming) {
      revealStore.set(id, { revealed: target, done: true });
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    } else {
      revealStore.set(id, { revealed, done: false });
    }
  }, [id, revealed, target, streaming]);

  const caughtUp = revealed >= target;
  return {
    shown: text.slice(0, revealed),
    // Still "revealing" (cursor + fade) while there is unshown text, or while the
    // stream is open and we've caught up (more bursts may follow).
    revealing: !caughtUp || streaming,
  };
}

// Assistant response: progressively revealed Markdown with a trailing cursor while
// it writes. The per-word `.word-fade` softens each newly-revealed word.
function AssistantMessage({
  entry,
}: {
  entry: Extract<TranscriptEntry, { kind: 'assistant' }>;
}): JSX.Element {
  const { shown, revealing } = useReveal(entry.id, entry.text, entry.streaming, entry.fromDelta ?? false);
  return (
    <div className="chat-msg chat-msg--assistant">
      <Markdown text={shown} animate={revealing} />
      {revealing && <span className="chat-msg__cursor" aria-label="streaming" />}
    </div>
  );
}

// Reasoning: same progressive reveal over the faded, collapsible plain-text block
// so "thinking" writes out like the CLI instead of popping in.
function ReasoningBlock({
  entry,
}: {
  entry: Extract<TranscriptEntry, { kind: 'reasoning' }>;
}): JSX.Element {
  const { shown } = useReveal(entry.id, entry.text, entry.streaming ?? false, entry.fromDelta ?? false);
  return (
    <details className="chat-entry--reasoning" open>
      <summary className="chat-reasoning__summary">Reasoning</summary>
      <div className="chat-reasoning__text">{shown}</div>
    </details>
  );
}


export function ChatViewHost({
  workspace,
  findHotkeyScope = 'global',
}: ChatViewHostProps): JSX.Element {
  const [framesBySeq, setFramesBySeq] = useState<Map<number, EventFrame>>(() => new Map());
  // Optimistic, client-side user-message frames (the backend never emits them).
  const [localUserFrames, setLocalUserFrames] = useState<EventFrame[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [optimisticTurn, setOptimisticTurn] = useState(false);
  const [answeredRequests, setAnsweredRequests] = useState<Set<string>>(() => new Set());
  const [answerLabels, setAnswerLabels] = useState<Map<string, string>>(() => new Map());
  const [autoYes, setAutoYes] = useState(workspace.autoYes);
  const [activePage, setActivePage] = useState<ChatPage>('chat');
  // Live model selector: the list from ListModels and the optimistic local picks
  // (model + reasoning effort + context tier) until the daemon's usage frames
  // report the switch. All reset per session.
  const [models, setModels] = useState<ModelInfo[]>([]);
  const [selectedModelId, setSelectedModelId] = useState<string | undefined>(undefined);
  // The user's explicit reasoning-effort pick; undefined falls back to the active
  // model's default (see currentEffort below). Reset when the model changes.
  const [selectedEffort, setSelectedEffort] = useState<string | undefined>(undefined);
  // The context tier ('default' | 'long_context'). Undefined means no local pick
  // this mount, so currentContextTier can fall back to the restored 'model' frame
  // tier (then the 'default' tier). A user pick sets it explicitly.
  const [selectedContextTier, setSelectedContextTier] = useState<string | undefined>(undefined);
  // Mirrors the last list applied to `models` so the fetch can skip a no-op
  // dispatch (a direct-value setState React can eager-bail, unlike an updater).
  const modelsRef = useRef<ModelInfo[]>([]);

  const scrollRef = useRef<HTMLDivElement>(null);
  // The chat-view root, used to scope the Ctrl+F find hotkey to the
  // hovered/focused chat when findHotkeyScope is 'active' (grid tiles).
  const chatViewRef = useRef<HTMLElement>(null);
  // Inner wrapper around the transcript; a ResizeObserver on it keeps the view
  // pinned to the bottom as the reveal/markdown grows the content height (a frame
  // change is NOT emitted while the typewriter animates, so the frame-keyed layout
  // effect below cannot follow that growth on its own).
  const contentRef = useRef<HTMLDivElement>(null);
  const shouldStickToBottom = useRef(true);
  // Show the floating "scroll to bottom" button when the user is more than ~2
  // viewport heights above the bottom.
  const [showScrollButton, setShowScrollButton] = useState(false);
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

  // In-chat find (Ctrl/Cmd+F). The transcript lives in the DOM, so we highlight
  // matches with the CSS Custom Highlight API (see useTranscriptSearch). Recompute
  // on entry-count changes so new messages are searchable without resetting the
  // user's position on every streaming reveal tick.
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const search = useTranscriptSearch(
    contentRef,
    searchQuery,
    searchOpen,
    transcript.entries.length,
  );
  const closeSearch = useCallback(() => {
    setSearchOpen(false);
    setSearchQuery('');
  }, []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (!(e.ctrlKey || e.metaKey) || (e.key !== 'f' && e.key !== 'F')) return;
      const responds =
        findHotkeyScope === 'global' ||
        !!chatViewRef.current?.matches(':hover, :focus-within');
      if (!responds) return;
      e.preventDefault();
      setActivePage('chat');
      setSearchOpen(true);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [findHotkeyScope]);

  // The active selection prefers the optimistic local pick (instant feedback on a
  // user switch), then the daemon's persisted 'model' frame (restores model +
  // effort + tier after a resume/remount), then the usage frame's model / the
  // model's own defaults. modelState is last-write-wins from the stream.
  const modelState = transcript.modelState;
  const currentModelId = selectedModelId ?? modelState?.model ?? transcript.usage?.model;
  // Resolve the active model from the list to drive effort support + defaults.
  const currentModel = models.find((model) => model.id === currentModelId);
  // Effort/context fall back to the persisted 'model' frame, then the active
  // model's default / the 'default' tier, until the user picks one explicitly.
  const currentEffort = selectedEffort ?? modelState?.effort ?? currentModel?.defaultEffort;
  const currentContextTier = selectedContextTier ?? modelState?.contextTier ?? 'default';
  // Adjust-state-during-render: when the active model changes (a live switch or
  // the first usage frame), drop any explicit effort pick so currentEffort falls
  // back to the new model's default. Mirrors the AutoYes seed pattern above; no
  // effect required.
  const [effortModelSeed, setEffortModelSeed] = useState<string | undefined>(currentModelId);
  if (effortModelSeed !== currentModelId) {
    setEffortModelSeed(currentModelId);
    setSelectedEffort(undefined);
  }
  const usageModel = transcript.usage?.model?.trim() || undefined;
  const contextLabel = formatContextPercent(transcript.usage);
  const aic = transcript.usage?.aic;
  const aicLabel =
    typeof aic === 'number' && Number.isFinite(aic) && aic > 0 ? `${formatAic(aic)} AIC` : undefined;
  const composerInfo =
    usageModel || contextLabel || aicLabel ? (
      <>
        {usageModel && <span className="chat-composer__info-model">{usageModel}</span>}
        {usageModel && contextLabel && (
          <span className="chat-composer__info-sep" aria-hidden="true">
            {'\u00B7'}
          </span>
        )}
        {contextLabel && <span className="chat-composer__info-context">{contextLabel}</span>}
        {(usageModel || contextLabel) && aicLabel && (
          <span className="chat-composer__info-sep" aria-hidden="true">
            {'\u00B7'}
          </span>
        )}
        {aicLabel && <span className="chat-composer__info-aic">{aicLabel}</span>}
      </>
    ) : undefined;

  // CLI-style activity label shown (with a spinner) on the left above the box,
  // only while a turn is in progress. Derived from the live transcript.
  const activityStatus = turnInProgress ? deriveActivityStatus(transcript.entries) : undefined;

  // Subscribe to the rich event stream for this chat; re-subscribe when the chat
  // (session) changes and tear down on unmount. Mirrors TranscriptView.
  useEffect(() => {
    // Seed the transcript from the per-session frame cache so a re-subscribe
    // (the rich stream closing and reopening) keeps the existing transcript on
    // screen instead of blanking it. The since=0 replay below then merges
    // idempotently by seq, so the transcript stays put rather than popping in.
    const cached = richFrameCache.get(workspace.sessionName) ?? new Map<number, EventFrame>();
    richFrameCache.set(workspace.sessionName, cached);
    if (cached.size > 0) {
      // A non-empty cache means we are re-subscribing over an existing
      // transcript. Log it lightly (no renderer logger exists) so future
      // stream churn stays visible in the devtools console.
      console.debug('[ChatViewHost] rich re-subscribe', {
        session: workspace.sessionName,
        cachedFrames: cached.size,
      });
    }
    setFramesBySeq(new Map(cached));
    setLocalUserFrames([]);
    setStreamError(null);
    setOptimisticTurn(false);
    setAnsweredRequests(new Set());
    setAnswerLabels(new Map());
    localCounter.current = 0;
    shouldStickToBottom.current = true;
    setShowScrollButton(false);

    const addFrame = (frame: EventFrame): void => {
      // Keep the per-session cache current (live deltas included) so a later
      // re-subscribe re-seeds from the full transcript, not just the last
      // replay. The framesBySeq guard below stays reference-idempotent.
      cached.set(frame.seq, frame);
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

  // Fetch the selectable models for this session (live model selector). Resets
  // the list + optimistic pick when the session changes; a stale resolve from a
  // previous session is dropped via the cancelled flag.
  useEffect(() => {
    let cancelled = false;
    // Clear the previous session's list/pick. Guard the dispatch so an
    // already-empty list does not schedule a no-op update (avoids act() noise).
    if (modelsRef.current.length > 0) {
      modelsRef.current = [];
      setModels([]);
    }
    setSelectedModelId(undefined);
    // Reset the per-session effort/context picks so they re-seed from the new
    // session's restored 'model' frame (then the active model's default effort /
    // the 'default' tier). Undefined = no local pick yet this mount.
    setSelectedEffort(undefined);
    setSelectedContextTier(undefined);
    void window.cs
      .listModels(workspace.sessionName)
      .then((list) => {
        // Skip entirely when unchanged so an empty result over an empty list
        // never dispatches (and never warns about an update outside act()).
        if (cancelled || sameModelList(modelsRef.current, list)) return;
        modelsRef.current = list;
        setModels(list);
      })
      .catch((error: unknown) => {
        if (!cancelled) setStreamError(error instanceof Error ? error.message : String(error));
      });
    return () => {
      cancelled = true;
    };
  }, [workspace.sessionName]);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && shouldStickToBottom.current) {
      el.scrollTop = el.scrollHeight;
    }
  }, [frames.length, streamError, activePage]);

  // Follow the content as it grows. The typewriter reveal (and markdown reflow /
  // image loads) grows the transcript height WITHOUT a frame change, so the
  // frame-keyed layout effect above can't track it -- a ResizeObserver on the
  // content wrapper re-pins to the bottom whenever we're stuck there. Guarded for
  // jsdom, which lacks ResizeObserver. Re-runs when the page changes because the
  // content wrapper only exists on the chat page.
  useEffect(() => {
    const content = contentRef.current;
    const el = scrollRef.current;
    if (!content || !el || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      if (shouldStickToBottom.current) el.scrollTop = el.scrollHeight;
      const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
      shouldStickToBottom.current = distance < SCROLL_SLOP;
      setShowScrollButton(el.clientHeight > 0 && distance > el.clientHeight * 2);
    });
    observer.observe(content);
    return () => observer.disconnect();
  }, [activePage]);

  // A scroll detaches stick-to-bottom the moment the user moves up, and re-attaches
  // only once they return to the bottom (within SCROLL_SLOP). The FAB appears once
  // they are more than ~2 viewport heights above the bottom.
  const onScroll = (): void => {
    const el = scrollRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    shouldStickToBottom.current = distance < SCROLL_SLOP;
    setShowScrollButton(el.clientHeight > 0 && distance > el.clientHeight * 2);
  };

  // Smooth-scroll back to the latest output (instant under reduced motion), re-arm
  // stick-to-bottom and hide the FAB.
  const scrollToBottom = (): void => {
    const el = scrollRef.current;
    if (!el) return;
    shouldStickToBottom.current = true;
    setShowScrollButton(false);
    el.scrollTo({ top: el.scrollHeight, behavior: revealAnimationEnabled() ? 'smooth' : 'auto' });
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
  const handleSend = (text: string, attachments: string[]): void => {
    const message = text.trim();
    if (!message && attachments.length === 0) return;
    const seq = nextLocalSeq();
    const bubbleText = composeUserBubble(message, attachments);
    setLocalUserFrames((current) => [...current, { seq, kind: USER_LOCAL_KIND, text: bubbleText }]);
    setOptimisticTurn(true);
    void window.cs
      .sendMessage(workspace.sessionName, message, attachments)
      .catch((error: unknown) => {
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

  // Switch the session's model (and reasoning effort + context tier) live.
  // Optimistically reflect the picks in the selector, then let the daemon's usage
  // frames reconcile; revert to the previous picks on failure.
  const applyModel = (modelId: string, effort: string, contextTier: string): void => {
    const prevModelId = selectedModelId;
    const prevEffort = selectedEffort;
    const prevContextTier = selectedContextTier;
    setSelectedModelId(modelId);
    setSelectedEffort(effort);
    setSelectedContextTier(contextTier);
    void window.cs
      .setModel(workspace.sessionName, modelId, effort, contextTier)
      .catch((error: unknown) => {
        setSelectedModelId(prevModelId);
        setSelectedEffort(prevEffort);
        setSelectedContextTier(prevContextTier);
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
    <section className="chat-view" aria-label="Chat conversation" ref={chatViewRef}>
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

          <div className="chat-view__scroll-wrap">
            {searchOpen && (
              <ChatSearchBar
                query={searchQuery}
                onQueryChange={setSearchQuery}
                matchCount={search.matchCount}
                activeOrdinal={search.activeOrdinal}
                onNext={search.next}
                onPrev={search.prev}
                onClose={closeSearch}
              />
            )}
            <div ref={scrollRef} className="chat-view__scroll" onScroll={onScroll}>
              <div ref={contentRef} className="chat-view__scroll-content">
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
            </div>
            <button
              type="button"
              className={`chat-view__scroll-btn${showScrollButton ? ' chat-view__scroll-btn--visible' : ''}`}
              aria-label="Scroll to bottom"
              aria-hidden={!showScrollButton}
              tabIndex={showScrollButton ? 0 : -1}
              onClick={scrollToBottom}
            >
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
                <path
                  d="M4 6l4 4 4-4"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.75"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            </button>
          </div>

          <div className="chat-view__composer-slot">
            <Composer
              turnInProgress={turnInProgress}
              onSend={handleSend}
              onStop={handleStop}
              status={activityStatus}
              info={composerInfo}
              models={models}
              currentModelId={currentModelId}
              currentEffort={currentEffort}
              currentContextTier={currentContextTier}
              onApplyModel={applyModel}
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

      {activePage === 'mcp' && (
        <div className="chat-view__body chat-view__page">
          <McpPage servers={transcript.mcpServers} />
        </div>
      )}

      {activePage === 'skills' && (
        <div className="chat-view__body chat-view__page">
          <SkillsPage skills={transcript.skills} />
        </div>
      )}

      {activePage === 'instructions' && (
        <div className="chat-view__body chat-view__page">
          <InstructionsPage instructions={transcript.instructions} />
        </div>
      )}

      {activePage === 'agents' && (
        <div className="chat-view__body chat-view__page">
          <AgentsPage agents={transcript.agents} />
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
}): JSX.Element | null {
  switch (entry.kind) {
    case 'user':
      return (
        <div className="chat-msg chat-msg--user">
          <div className="chat-msg__bubble">{entry.text}</div>
        </div>
      );
    case 'assistant':
      // Progressive typewriter reveal (decoupled from the SDK's bursty deltas);
      // history / resumed messages snap to full. See AssistantMessage / useReveal.
      return <AssistantMessage entry={entry} />;
    case 'reasoning':
      // CLI-style: a subtle faded header over faded muted-italic text, default
      // expanded but still collapsible. No bubble (no border/background). Streamed
      // reasoning writes out via the same reveal.
      return <ReasoningBlock entry={entry} />;
    case 'tool':
      // CLI-style: a clean single line -- a small status dot, the tool name,
      // then faded args and a faded result. No box, no Running/Done badge.
      return (
        <div className="chat-tool">
          {autoYes && entry.permission && (
            <span className="chat-tool__perm" tabIndex={0} role="button" aria-label="Auto-approved permission">
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
                <path
                  d="M8 1.75 3 3.6v3.55c0 3.15 2.05 5.75 5 7.1 2.95-1.35 5-3.95 5-7.1V3.6L8 1.75Z"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinejoin="round"
                />
                <path
                  d="m5.7 7.9 1.45 1.45 3.2-3.35"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
              <span className="chat-tool__perm-popover" role="tooltip">
                {entry.permission.toolName && (
                  <span className="chat-tool__perm-title">{entry.permission.toolName}</span>
                )}
                <span>{entry.permission.question}</span>
              </span>
            </span>
          )}
          <span className={`chat-tool__dot chat-tool__dot--${entry.status}`} aria-hidden="true" />
          <span className="chat-tool__name">{entry.toolName}</span>
          {entry.mcpServer && <span className="chat-tool__server">{entry.mcpServer}</span>}
          {entry.args && <span className="chat-tool__args">{entry.args}</span>}
          {entry.result && <span className="chat-tool__result">{entry.result}</span>}
        </div>
      );
    case 'permission': {
      // Answered when the stream replayed a permission.resolved (survives a fresh
      // mount/replay) OR the optimistic local click marked it (instant feedback).
      const localAnswered = entry.requestId ? answeredRequests.has(entry.requestId) : false;
      const localLabel = entry.requestId ? answerLabels.get(entry.requestId) : undefined;
      if (autoYes && entry.linkedToTool) return null;
      return (
        <ChatPermissionEntry
          entry={entry}
          autoYes={autoYes}
          answered={entry.answered === true || localAnswered}
          answerLabel={localLabel ?? entry.answerLabel}
          onRespond={onRespondPermission}
        />
      );
    }
    case 'input': {
      // Same dual source as permission: stream-resolved (replay) OR local click.
      const localAnswered = entry.requestId ? answeredRequests.has(entry.requestId) : false;
      const localLabel = entry.requestId ? answerLabels.get(entry.requestId) : undefined;
      return (
        <ChatUserInputEntry
          entry={entry}
          answered={entry.answered === true || localAnswered}
          answerLabel={localLabel ?? entry.answerLabel}
          onRespond={onRespondUserInput}
        />
      );
    }
    case 'idle':
      if (entry.aborted) {
        return (
          <div className="chat-entry--idle chat-entry--idle--canceled">
            <span className="chat-idle-dot chat-idle-dot--purple" aria-hidden="true" />
            <span>Operation canceled by user</span>
          </div>
        );
      }
      // A tiny faded centered turn marker (the CLI shows no usage line at all).
      return <div className="chat-entry--idle">{formatCompletedTime(entry.timestamp)}</div>;
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
      <div className="chat-request__header">
        <strong>Permission requested</strong>
        {answered && answerLabel && <span className="chat-request__state">{answerLabel}</span>}
      </div>
      {detail && <span className="chat-request__detail">{detail}</span>}
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