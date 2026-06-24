// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
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
});
