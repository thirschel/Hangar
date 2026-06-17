import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const fsMock = vi.hoisted(() => ({
  appendFileSync: vi.fn(),
  mkdirSync: vi.fn(),
}));

vi.mock('node:fs', () => fsMock);
vi.mock('node:os', () => ({
  default: { homedir: () => 'C:\\Users\\tester' },
  homedir: () => 'C:\\Users\\tester',
}));

import { log } from '../logger';

describe('log', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('info calls console.log', () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {});

    log.info('hello', 'world');

    expect(spy).toHaveBeenCalledWith('hello', 'world');
  });

  it('error calls console.error', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});

    log.error('boom');

    expect(spy).toHaveBeenCalledWith('boom');
  });

  it('formats Error objects using the stack trace', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const err = new Error('boom');
    err.stack = 'STACK TRACE';

    log.error(err);

    expect(fsMock.appendFileSync).toHaveBeenCalledOnce();
    expect(String(fsMock.appendFileSync.mock.calls[0][1])).toContain('STACK TRACE');
  });

  it('JSON-stringifies non-string args', () => {
    vi.spyOn(console, 'log').mockImplementation(() => {});

    log.info({ ok: true }, 42);

    expect(fsMock.appendFileSync).toHaveBeenCalledOnce();
    expect(String(fsMock.appendFileSync.mock.calls[0][1])).toContain('{"ok":true} 42');
  });
});
