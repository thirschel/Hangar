import { EventEmitter } from 'node:events';
import crypto from 'node:crypto';
import { execFileSync } from 'node:child_process';
import type net from 'node:net';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { frame, FrameDecoder, ControlClient, requestHostShutdown, killHostProcess } from '../host-client';

vi.mock('node:child_process', () => ({
  execFileSync: vi.fn(),
  spawn: vi.fn(),
}));

class MockSocket extends EventEmitter {
  public readonly writes: Buffer[] = [];
  public ended = false;
  public writeError: Error | null = null;

  public write(
    chunk: string | Uint8Array,
    callback?: ((error: Error | null | undefined) => void) | undefined,
  ): boolean {
    this.writes.push(Buffer.isBuffer(chunk) ? Buffer.from(chunk) : Buffer.from(chunk));
    callback?.(this.writeError ?? undefined);
    return true;
  }

  public end(): this {
    this.ended = true;
    return this;
  }

  public destroy(error?: Error): this {
    if (error) {
      this.emit('error', error);
    }
    this.emit('close');
    return this;
  }
}

describe('frame', () => {
  it('encodes a payload with a 4-byte big-endian length prefix', () => {
    const payload = Buffer.from('hello', 'utf8');

    const encoded = frame(payload);

    expect(encoded.readUInt32BE(0)).toBe(payload.length);
    expect(encoded.subarray(4)).toEqual(payload);
  });

  it('handles an empty payload', () => {
    const encoded = frame(Buffer.alloc(0));

    expect(encoded).toEqual(Buffer.from([0, 0, 0, 0]));
  });
});

describe('FrameDecoder', () => {
  it('delivers a complete frame in a single push', () => {
    const onFrame = vi.fn();
    const decoder = new FrameDecoder(onFrame);
    const payload = Buffer.from('single-frame', 'utf8');

    decoder.push(frame(payload));

    expect(onFrame).toHaveBeenCalledWith(payload);
  });

  it('reassembles a frame split across multiple pushes', () => {
    const onFrame = vi.fn();
    const decoder = new FrameDecoder(onFrame);
    const encoded = frame(Buffer.from('split-frame', 'utf8'));

    decoder.push(encoded.subarray(0, 2));
    decoder.push(encoded.subarray(2, 7));
    decoder.push(encoded.subarray(7));

    expect(onFrame).toHaveBeenCalledOnce();
    expect(onFrame).toHaveBeenCalledWith(Buffer.from('split-frame', 'utf8'));
  });

  it('delivers multiple frames concatenated in a single push', () => {
    const onFrame = vi.fn();
    const decoder = new FrameDecoder(onFrame);
    const first = Buffer.from('first', 'utf8');
    const second = Buffer.from('second', 'utf8');

    decoder.push(Buffer.concat([frame(first), frame(second)]));

    expect(onFrame.mock.calls).toEqual([[first], [second]]);
  });

  it('throws on a frame exceeding MAX_FRAME', () => {
    const decoder = new FrameDecoder(() => {});
    const header = Buffer.alloc(4);
    header.writeUInt32BE((16 << 20) + 1, 0);

    expect(() => decoder.push(header)).toThrow('frame too large');
  });

  it('reassembles a single frame delivered across many 1-byte chunks', () => {
    const received: Buffer[] = [];
    const decoder = new FrameDecoder((payload) => received.push(Buffer.from(payload)));
    const payload = Buffer.from('payload-delivered-one-byte-at-a-time', 'utf8');
    const encoded = frame(payload);

    for (const byte of encoded) {
      decoder.push(Buffer.from([byte]));
    }

    expect(received).toHaveLength(1);
    expect(received[0].equals(payload)).toBe(true);
  });

  it('reads a 4-byte length prefix that is split across chunks', () => {
    const onFrame = vi.fn();
    const decoder = new FrameDecoder(onFrame);
    const payload = Buffer.from('split-length-prefix', 'utf8');
    const encoded = frame(payload);

    // Only part of the 4-byte length prefix has arrived — not enough to decode.
    decoder.push(encoded.subarray(0, 1));
    decoder.push(encoded.subarray(1, 3));
    expect(onFrame).not.toHaveBeenCalled();

    // The 4th length byte completes the prefix, but the payload is still missing.
    decoder.push(encoded.subarray(3, 4));
    expect(onFrame).not.toHaveBeenCalled();

    // The remaining payload bytes complete the frame.
    decoder.push(encoded.subarray(4));
    expect(onFrame).toHaveBeenCalledOnce();
    expect(onFrame).toHaveBeenCalledWith(payload);
  });

  it('delivers several frames (including an empty payload) from one chunk', () => {
    const onFrame = vi.fn();
    const decoder = new FrameDecoder(onFrame);
    const first = Buffer.from('alpha', 'utf8');
    const empty = Buffer.alloc(0);
    const third = Buffer.from('gamma', 'utf8');

    decoder.push(Buffer.concat([frame(first), frame(empty), frame(third)]));

    expect(onFrame.mock.calls).toEqual([[first], [empty], [third]]);
  });

  it('reassembles a large (~1 MiB) frame split across many chunks without error', () => {
    const received: Buffer[] = [];
    const decoder = new FrameDecoder((payload) => received.push(Buffer.from(payload)));
    const payload = crypto.randomBytes(1 << 20);
    const encoded = frame(payload);
    const chunkSize = 1024;

    expect(() => {
      for (let offset = 0; offset < encoded.length; offset += chunkSize) {
        decoder.push(encoded.subarray(offset, Math.min(offset + chunkSize, encoded.length)));
      }
    }).not.toThrow();

    expect(received).toHaveLength(1);
    expect(received[0].length).toBe(payload.length);
    expect(received[0].equals(payload)).toBe(true);
  });
});

describe('ControlClient', () => {
  it('resolves with a parsed JSON response', async () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);
    const pending = client.call({ method: 'Hello' });

    expect(socket.writes).toHaveLength(1);
    expect(JSON.parse(socket.writes[0].subarray(4).toString('utf8'))).toMatchObject({
      id: 1,
      method: 'Hello',
    });

    socket.emit(
      'data',
      frame(Buffer.from(JSON.stringify({ id: 1, ok: true, content: 'ready' }), 'utf8')),
    );

    await expect(pending).resolves.toEqual({ id: 1, ok: true, content: 'ready' });
  });

  it('rejects when the socket closes before a response arrives', async () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);
    const pending = client.call({ method: 'Hello' });

    socket.emit('close');

    await expect(pending).rejects.toThrow('control pipe closed');
  });

  it('rejects immediately if already closed', async () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);

    socket.emit('close');

    await expect(client.call({ method: 'Hello' })).rejects.toThrow('control pipe is closed');
  });

  it('close sets isClosed to true', () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);

    client.close();

    expect(client.isClosed()).toBe(true);
    expect(socket.ended).toBe(true);
  });

  it('matches responses by id regardless of arrival order', async () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);

    const a = client.call({ method: 'Hello' }); // id 1
    const b = client.call({ method: 'ListWorkspaces' }); // id 2

    // Respond to id 2 first, then id 1: FIFO matching would mis-deliver these.
    socket.emit('data', frame(Buffer.from(JSON.stringify({ id: 2, ok: true, content: 'B' }), 'utf8')));
    socket.emit('data', frame(Buffer.from(JSON.stringify({ id: 1, ok: true, content: 'A' }), 'utf8')));

    await expect(a).resolves.toMatchObject({ id: 1, content: 'A' });
    await expect(b).resolves.toMatchObject({ id: 2, content: 'B' });
  });

  it('rejects a call that times out', async () => {
    vi.useFakeTimers();
    try {
      const socket = new MockSocket();
      const client = new ControlClient(socket as unknown as net.Socket, 50);
      const pending = client.call({ method: 'Hello' });
      const assertion = expect(pending).rejects.toThrow(/timed out after 50ms/);
      await vi.advanceTimersByTimeAsync(50);
      await assertion;
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores a late response for an already-timed-out call', async () => {
    vi.useFakeTimers();
    try {
      const socket = new MockSocket();
      const client = new ControlClient(socket as unknown as net.Socket, 50);

      const a = client.call({ method: 'Hello' }); // id 1
      const aAssert = expect(a).rejects.toThrow(/timed out/);
      await vi.advanceTimersByTimeAsync(50);
      await aAssert;

      const b = client.call({ method: 'ListWorkspaces' }); // id 2
      // A late reply for the timed-out id 1 must NOT resolve id 2.
      socket.emit('data', frame(Buffer.from(JSON.stringify({ id: 1, ok: true, content: 'late-A' }), 'utf8')));
      socket.emit('data', frame(Buffer.from(JSON.stringify({ id: 2, ok: true, content: 'B' }), 'utf8')));

      await expect(b).resolves.toMatchObject({ id: 2, content: 'B' });
    } finally {
      vi.useRealTimers();
    }
  });

  it('marks itself closed when a write fails so getControlClient can reconnect', async () => {
    const socket = new MockSocket();
    socket.writeError = new Error('write EOF');
    const client = new ControlClient(socket as unknown as net.Socket);
    await expect(client.call({ method: 'Hello' })).rejects.toThrow(/write EOF/);
    expect(client.isClosed()).toBe(true);
  });

  it('marks itself closed on a socket error', () => {
    const socket = new MockSocket();
    const client = new ControlClient(socket as unknown as net.Socket);
    socket.emit('error', new Error('pipe error'));
    expect(client.isClosed()).toBe(true);
  });
});

describe('requestHostShutdown', () => {
  it('sends a Shutdown RPC and returns true when the host acknowledges', async () => {
    const call = vi.fn().mockResolvedValue({ id: 1, ok: true });
    const client = { call, isClosed: () => false };

    await expect(requestHostShutdown(client)).resolves.toBe(true);
    expect(call).toHaveBeenCalledWith({ method: 'Shutdown' });
  });

  it('returns false without sending anything when the client is already closed', async () => {
    const call = vi.fn();
    const client = { call, isClosed: () => true };

    await expect(requestHostShutdown(client)).resolves.toBe(false);
    expect(call).not.toHaveBeenCalled();
  });

  it('returns false when the host replies not-ok', async () => {
    const call = vi.fn().mockResolvedValue({ id: 1, ok: false, error: 'boom' });
    const client = { call, isClosed: () => false };

    await expect(requestHostShutdown(client)).resolves.toBe(false);
  });

  it('returns false when the Shutdown RPC rejects (e.g. pipe closed)', async () => {
    const call = vi.fn().mockRejectedValue(new Error('control pipe closed'));
    const client = { call, isClosed: () => false };

    await expect(requestHostShutdown(client)).resolves.toBe(false);
  });

  it('returns false when the host does not respond before the timeout', async () => {
    vi.useFakeTimers();
    try {
      // A call that never settles: only the timeout can resolve the race.
      const call = vi.fn().mockReturnValue(new Promise<never>(() => {}));
      const client = { call, isClosed: () => false };

      const pending = requestHostShutdown(client, 50);
      await vi.advanceTimersByTimeAsync(50);

      await expect(pending).resolves.toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('killHostProcess', () => {
  beforeEach(() => {
    vi.mocked(execFileSync).mockReset();
  });

  it('force-kills the host process tree via taskkill for a valid pid', () => {
    vi.mocked(execFileSync).mockReturnValue(Buffer.from(''));

    expect(killHostProcess(4242)).toBe(true);
    expect(execFileSync).toHaveBeenCalledWith(
      'taskkill',
      ['/PID', '4242', '/T', '/F'],
      expect.objectContaining({ windowsHide: true }),
    );
  });

  it('returns false and never spawns taskkill for an invalid pid', () => {
    expect(killHostProcess(0)).toBe(false);
    expect(killHostProcess(-1)).toBe(false);
    expect(killHostProcess(Number.NaN)).toBe(false);
    expect(execFileSync).not.toHaveBeenCalled();
  });

  it('returns false when taskkill throws (process already gone)', () => {
    vi.mocked(execFileSync).mockImplementation(() => {
      throw new Error('process not found');
    });

    expect(killHostProcess(4242)).toBe(false);
  });
});
