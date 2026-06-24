// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { EventFrame } from '../../../main/host-client';
import { TranscriptView } from '../components/TranscriptView';

describe('TranscriptView', () => {
  let richFrameCallback:
    | ((data: { session: string; frame: EventFrame }) => void)
    | undefined;

  beforeEach(() => {
    richFrameCallback = undefined;
    window.cs.openRichStream = vi
      .fn()
      .mockResolvedValue({ attachPipe: 'rich_mock', attachToken: 'token_mock' });
    window.cs.closeRichStream = vi.fn().mockResolvedValue(undefined);
    window.cs.onRichFrame = vi.fn((callback: (data: { session: string; frame: EventFrame }) => void) => {
      richFrameCallback = callback;
      return () => {};
    });
    window.cs.onRichError = vi.fn().mockReturnValue(() => {});
    window.cs.respondPermission = vi.fn().mockResolvedValue(undefined);
    window.cs.respondUserInput = vi.fn().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders streamed assistant text and a completed tool card', async () => {
    render(<TranscriptView sessionName="rich-session" />);

    await vi.waitFor(() => expect(window.cs.openRichStream).toHaveBeenCalledWith('rich-session', 0));
    expect(richFrameCallback).toBeDefined();

    const frames: EventFrame[] = [
      { seq: 1, kind: 'assistant.delta', text: 'Hello ' },
      { seq: 2, kind: 'assistant.delta', text: 'there' },
      { seq: 3, kind: 'tool.start', toolName: 'read_file', mcpServer: 'filesystem', requestId: 'tool-1' },
      {
        seq: 4,
        kind: 'tool.complete',
        toolName: 'read_file',
        mcpServer: 'filesystem',
        requestId: 'tool-1',
        status: 'ok',
      },
      { seq: 5, kind: 'assistant.message', text: 'Hello there' },
      { seq: 6, kind: 'idle' },
    ];

    await act(async () => {
      for (const frame of frames) {
        richFrameCallback?.({ session: 'rich-session', frame });
      }
    });

    expect(screen.getByText('Hello there')).toBeInTheDocument();
    expect(screen.getByText('read_file')).toBeInTheDocument();
    expect(screen.getByText('filesystem')).toBeInTheDocument();
    expect(screen.getByText('Done')).toBeInTheDocument();
    expect(screen.getByText('Turn complete.')).toBeInTheDocument();
  });

  it('filters frames for other sessions', async () => {
    render(<TranscriptView sessionName="rich-session" />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({ session: 'other-session', frame: { seq: 1, kind: 'assistant.message', text: 'Nope' } });
    });

    expect(screen.queryByText('Nope')).not.toBeInTheDocument();
  });

  it('renders MCP server statuses and applies the latest status per server', async () => {
    render(<TranscriptView sessionName="rich-session" />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 1, kind: 'mcp.status', mcpServer: 'github', status: 'connected' },
      });
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 2, kind: 'mcp.status', mcpServer: 'broken', status: 'failed', error: 'No token' },
      });
    });

    expect(screen.getByLabelText('MCP server status')).toBeInTheDocument();
    expect(screen.getByText('github')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('broken')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: { seq: 3, kind: 'mcp.status', mcpServer: 'github', status: 'needs-auth' },
      });
    });

    expect(screen.getAllByText('github')).toHaveLength(1);
    expect(screen.getByText('Needs auth')).toBeInTheDocument();
    expect(screen.queryByText('Connected')).not.toBeInTheDocument();
  });

  it('answers a permission request and disables its controls', async () => {
    render(<TranscriptView sessionName="rich-session" autoYes={false} />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'permission.requested',
          requestId: 'perm-1',
          question: 'Allow shell command?',
          choices: ['approve', 'reject'],
        },
      });
    });

    const approve = screen.getByRole('button', { name: 'Approve' });
    const reject = screen.getByRole('button', { name: 'Reject' });

    fireEvent.click(approve);

    expect(window.cs.respondPermission).toHaveBeenCalledWith('rich-session', 'perm-1', 'approve');
    expect(approve).toBeDisabled();
    expect(reject).toBeDisabled();
    expect(screen.getByText('approved')).toBeInTheDocument();
  });

  it('answers a user input request with a choice', async () => {
    render(<TranscriptView sessionName="rich-session" />);
    await vi.waitFor(() => expect(richFrameCallback).toBeDefined());

    await act(async () => {
      richFrameCallback?.({
        session: 'rich-session',
        frame: {
          seq: 1,
          kind: 'user_input.requested',
          requestId: 'input-1',
          question: 'Pick an option',
          choices: ['Alpha', 'Beta'],
        },
      });
    });

    fireEvent.click(screen.getByRole('button', { name: 'Beta' }));

    expect(window.cs.respondUserInput).toHaveBeenCalledWith('rich-session', 'input-1', 'Beta', false);
  });
});
