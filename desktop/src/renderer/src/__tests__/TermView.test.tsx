// @vitest-environment jsdom
import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// A shared fit() spy used for ordering assertions. Hoisted so the addon-fit mock
// factory (which vitest hoists above imports) can close over it. fit() writes the
// FITTED size onto the terminal it was loaded into, mimicking the real FitAddon
// adopting the measured pane.
const { fitSpy, writelnSpy, writeSpy, pasteSpy, clearSelectionSpy, keyHandlerRef, selectionState } =
  vi.hoisted(() => ({
    fitSpy: vi.fn(function (this: { term: { cols: number; rows: number } }) {
      this.term.cols = 100;
      this.term.rows = 40;
    }),
    writelnSpy: vi.fn(),
    writeSpy: vi.fn(),
    pasteSpy: vi.fn(),
    clearSelectionSpy: vi.fn(),
    keyHandlerRef: { handler: undefined as ((e: KeyboardEvent) => boolean) | undefined },
    selectionState: { text: '' },
  }));

// xterm pulls in a CSS side-effect import that jsdom cannot parse.
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));

// Minimal xterm stand-in covering every term.* / buffer.* / modes.* access in
// TermView. cols/rows start at the 120x30 constructor default so the test can
// prove the fit overwrote them to 100x40 BEFORE history priming and attach.
vi.mock('@xterm/xterm', () => {
  class FakeTerminal {
    cols = 120;
    rows = 30;
    buffer = { active: { viewportY: 0, baseY: 0, type: 'normal' } };
    modes = { mouseTrackingMode: 'none' };
    loadAddon(addon: { term?: FakeTerminal }): void {
      addon.term = this;
    }
    open(): void {}
    focus(): void {}
    reset(): void {}
    scrollToBottom(): void {}
    scrollToLine(): void {}
    clearSelection(): void {
      clearSelectionSpy();
    }
    paste(data?: string): void {
      pasteSpy(data);
    }
    dispose(): void {}
    writeln(data?: string): void {
      writelnSpy(data);
    }
    write(data: unknown, cb?: () => void): void {
      writeSpy(data);
      cb?.();
    }
    getSelection(): string {
      return selectionState.text;
    }
    hasSelection(): boolean {
      return false;
    }
    attachCustomKeyEventHandler(cb: (e: KeyboardEvent) => boolean): void {
      keyHandlerRef.handler = cb;
    }
    onData(): { dispose: () => void } {
      return { dispose: vi.fn() };
    }
    onScroll(): { dispose: () => void } {
      return { dispose: vi.fn() };
    }
  }
  return { Terminal: FakeTerminal };
});

vi.mock('@xterm/addon-fit', () => {
  class FakeFitAddon {
    term!: { cols: number; rows: number };
    fit = fitSpy;
  }
  return { FitAddon: FakeFitAddon };
});

import { TermView } from '../components/TermView';

describe('TermView startup ordering', () => {
  beforeEach(() => {
    // jsdom lacks a deterministic rAF and ResizeObserver; make them synchronous
    // so the double-rAF settle runs inline during render().
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe(): void {}
        unobserve(): void {}
        disconnect(): void {}
      },
    );
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      cb(0);
      return 0;
    });
    vi.stubGlobal('cancelAnimationFrame', () => {});

    // Per-test cs stubs that RESOLVE so attach's .then() chain runs.
    window.cs.getHistory = vi
      .fn()
      .mockResolvedValue({ ansi: '', altScreen: false, scrollbackLines: 0 });
    window.cs.attachSession = vi.fn().mockResolvedValue({ id: 0, ok: true });
    window.cs.resize = vi.fn();
    writelnSpy.mockClear();
    writeSpy.mockClear();
    pasteSpy.mockClear();
    clearSelectionSpy.mockClear();
    keyHandlerRef.handler = undefined;
    selectionState.text = '';
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('fits, then primes history, then attaches -- all at the fitted 100x40 size', async () => {
    const getHistory = vi.mocked(window.cs.getHistory);
    const attachSession = vi.mocked(window.cs.attachSession);

    await act(async () => {
      render(<TermView sessionName="ws_test" />);
    });
    await vi.waitFor(() => expect(attachSession).toHaveBeenCalled());

    // Order of operations: fit BEFORE getHistory BEFORE attachSession.
    expect(fitSpy.mock.invocationCallOrder[0]).toBeLessThan(getHistory.mock.invocationCallOrder[0]);
    expect(getHistory.mock.invocationCallOrder[0]).toBeLessThan(
      attachSession.mock.invocationCallOrder[0],
    );

    // Both calls carry the FITTED size, not the 120x30 constructor default.
    expect(attachSession).toHaveBeenCalledWith('ws_test', { cols: 100, rows: 40 });
    expect(getHistory).toHaveBeenCalledWith('ws_test', false, { cols: 100, rows: 40 });

    // Regression guard: nothing was sent at the pre-fit 120x30 default.
    expect(attachSession).not.toHaveBeenCalledWith('ws_test', { cols: 120, rows: 30 });
    expect(getHistory).not.toHaveBeenCalledWith('ws_test', expect.anything(), {
      cols: 120,
      rows: 30,
    });
  });

  it('writes a visible diagnostic when attach reports the agent already exited', async () => {
    window.cs.attachSession = vi.fn().mockResolvedValue({ id: 0, ok: true, alive: false, exitCode: 7 });

    await act(async () => {
      render(<TermView sessionName="ws_exited" />);
    });

    await vi.waitFor(() =>
      expect(writelnSpy).toHaveBeenCalledWith(
        expect.stringContaining('[agent process exited (code 7)'),
      ),
    );
    expect(writelnSpy).toHaveBeenCalledWith(
      expect.stringContaining('see host.log via Settings → Diagnostics'),
    );
  });

  it('writes incoming term:data bytes to the terminal and records diagnostics', async () => {
    // Capture the onData subscriber so the test can push a live chunk through it.
    let dataCb: ((d: { session: string; chunk: Uint8Array }) => void) | undefined;
    window.cs.onData = vi.fn((cb: (d: { session: string; chunk: Uint8Array }) => void) => {
      dataCb = cb;
      return () => {};
    });
    const diagSpy = vi.fn();
    window.cs.diag = diagSpy;

    await act(async () => {
      render(<TermView sessionName="ws_data" />);
    });
    await vi.waitFor(() => expect(dataCb).toBeDefined());

    const chunk = new Uint8Array([104, 105]); // "hi"
    await act(async () => {
      dataCb?.({ session: 'ws_data', chunk });
    });

    // The bytes reached term.write (the render path that was blank).
    expect(writeSpy).toHaveBeenCalledWith(chunk);
    // And the first-data diagnostic was recorded for desktop.log.
    expect(diagSpy).toHaveBeenCalledWith(
      'TermView first data',
      expect.objectContaining({ session: 'ws_data', bytes: 2 }),
      'info',
    );
  });

  it('ignores term:data for a different session and flags the mismatch', async () => {
    let dataCb: ((d: { session: string; chunk: Uint8Array }) => void) | undefined;
    window.cs.onData = vi.fn((cb: (d: { session: string; chunk: Uint8Array }) => void) => {
      dataCb = cb;
      return () => {};
    });
    const diagSpy = vi.fn();
    window.cs.diag = diagSpy;

    await act(async () => {
      render(<TermView sessionName="ws_self" />);
    });
    await vi.waitFor(() => expect(dataCb).toBeDefined());
    writeSpy.mockClear();

    await act(async () => {
      dataCb?.({ session: 'ws_other', chunk: new Uint8Array([1, 2, 3]) });
    });

    expect(writeSpy).not.toHaveBeenCalled();
    // Cross-session data is filtered out and reported (throttled, non-error) rather
    // than written to this pane's terminal.
    expect(diagSpy).toHaveBeenCalledWith(
      'TermView data session mismatch (filtered)',
      expect.objectContaining({ expected: 'ws_self', count: expect.any(Number) }),
      'info',
    );
    // …and never as a per-event error (the source of the prior log spam, #72).
    expect(diagSpy).not.toHaveBeenCalledWith(
      'TermView data session mismatch',
      expect.anything(),
      'error',
    );
  });

  it('copies the current selection to the clipboard on Ctrl+Shift+C', async () => {
    selectionState.text = 'selected text';
    const clipboardWrite = vi.fn().mockResolvedValue(undefined);
    window.cs.clipboardWrite = clipboardWrite;

    await act(async () => {
      render(<TermView sessionName="ws_copy" />);
    });
    await vi.waitFor(() => expect(keyHandlerRef.handler).toBeDefined());

    const handled = keyHandlerRef.handler?.({
      type: 'keydown',
      key: 'C',
      ctrlKey: true,
      shiftKey: true,
      altKey: false,
      preventDefault: vi.fn(),
    } as unknown as KeyboardEvent);

    // Routed through the main-process clipboard bridge, not navigator.clipboard, and
    // swallowed by xterm (returns false) so the agent never sees the keystroke.
    expect(clipboardWrite).toHaveBeenCalledWith('selected text');
    expect(handled).toBe(false);
  });

  it('reads the clipboard and pastes through xterm on Ctrl+Shift+V', async () => {
    const clipboardRead = vi.fn().mockResolvedValue('clipboard text');
    window.cs.clipboardRead = clipboardRead;

    await act(async () => {
      render(<TermView sessionName="ws_paste" />);
    });
    await vi.waitFor(() => expect(keyHandlerRef.handler).toBeDefined());

    const preventDefault = vi.fn();
    let handled: boolean | undefined;
    await act(async () => {
      handled = keyHandlerRef.handler?.({
        type: 'keydown',
        key: 'V',
        ctrlKey: true,
        shiftKey: true,
        altKey: false,
        preventDefault,
      } as unknown as KeyboardEvent);
    });

    // Chromium's native paste is suppressed (preventDefault) and the clipboard text is
    // routed back through term.paste so bracketed-paste wrapping is applied once.
    expect(handled).toBe(false);
    expect(preventDefault).toHaveBeenCalled();
    expect(clipboardRead).toHaveBeenCalled();
    await vi.waitFor(() => expect(pasteSpy).toHaveBeenCalledWith('clipboard text'));
  });
});
