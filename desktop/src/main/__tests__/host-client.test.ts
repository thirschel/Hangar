import { EventEmitter } from 'node:events';
import type net from 'node:net';
import { describe, expect, it, vi } from 'vitest';
import { frame, FrameDecoder, ControlClient } from '../host-client';

class MockSocket extends EventEmitter {
  public readonly writes: Buffer[] = [];
  public ended = false;

  public write(
    chunk: string | Uint8Array,
    callback?: ((error: Error | null | undefined) => void) | undefined,
  ): boolean {
    this.writes.push(Buffer.isBuffer(chunk) ? Buffer.from(chunk) : Buffer.from(chunk));
    callback?.(undefined);
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
});
