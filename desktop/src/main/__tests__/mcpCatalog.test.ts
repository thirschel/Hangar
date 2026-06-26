import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const fsMock = vi.hoisted(() => {
  const files = new Map<string, string>();
  return {
    files,
    readFileSync: vi.fn((file: string) => {
      const content = files.get(String(file));
      if (content === undefined) {
        const error = new Error(`ENOENT: ${file}`);
        Object.assign(error, { code: 'ENOENT' });
        throw error;
      }
      return content;
    }),
    writeFileSync: vi.fn((file: string, content: string) => {
      files.set(String(file), String(content));
    }),
    renameSync: vi.fn((from: string, to: string) => {
      const content = files.get(String(from));
      if (content === undefined) throw new Error(`ENOENT: ${from}`);
      files.set(String(to), content);
      files.delete(String(from));
    }),
    mkdirSync: vi.fn(),
  };
});

vi.mock('node:fs', () => ({
  readFileSync: fsMock.readFileSync,
  writeFileSync: fsMock.writeFileSync,
  renameSync: fsMock.renameSync,
  mkdirSync: fsMock.mkdirSync,
}));

vi.mock('../settings', () => ({
  csDir: () => 'C:\\Users\\tester\\.hangar',
}));

vi.mock('../logger', () => ({
  log: {
    error: vi.fn(),
    info: vi.fn(),
  },
}));

import {
  mcpJsonPath,
  readCatalog,
  removeServer,
  setRepoEnabled,
  upsertServer,
} from '../mcpCatalog';

const file = path.join('C:\\Users\\tester\\.hangar', 'mcp.json');

function seed(value: unknown): void {
  fsMock.files.set(file, JSON.stringify(value, null, 2));
}

function saved(): unknown {
  return JSON.parse(fsMock.files.get(file) ?? '{}') as unknown;
}

describe('mcpCatalog', () => {
  beforeEach(() => {
    fsMock.files.clear();
    vi.clearAllMocks();
  });

  it('uses ~/.hangar/mcp.json', () => {
    expect(mcpJsonPath()).toBe(file);
  });

  it('validates local command, http url, duplicate names, and clamps timeout', () => {
    expect(() => upsertServer('local', { type: 'local' })).toThrow(/command/);
    expect(() => upsertServer('remote', { type: 'http' })).toThrow(/url/);

    const catalog = upsertServer('local', {
      type: 'local',
      command: 'node',
      args: ['server.js', 42],
      env: { OK: 'yes', NO: 1 },
      tools: [],
      timeout: 999,
      unknown: true,
    });
    expect(catalog.servers.local).toEqual({
      type: 'local',
      command: 'node',
      args: ['server.js'],
      env: { OK: 'yes' },
      tools: ['*'],
      timeout: 600,
    });

    expect(() =>
      upsertServer(' local ', { type: 'local', command: 'node', timeout: 1 }),
    ).toThrow(/Duplicate/);
  });

  it('maps sse to http and clamps negative timeout to zero', () => {
    const catalog = upsertServer('remote', {
      type: 'sse',
      url: 'https://example.test/sse',
      headers: { Authorization: 'Bearer token', Bad: false },
      tools: ['a'],
      timeout: -10,
      command: 'ignored',
    });

    expect(catalog.servers.remote).toEqual({
      type: 'http',
      url: 'https://example.test/sse',
      headers: { Authorization: 'Bearer token' },
      tools: ['a'],
      timeout: 0,
    });
  });

  it('writes atomically and leaves the catalog file valid', () => {
    upsertServer('local', { type: 'local', command: 'npx', timeout: 5 });

    expect(fsMock.mkdirSync).toHaveBeenCalledWith('C:\\Users\\tester\\.hangar', {
      recursive: true,
    });
    expect(fsMock.writeFileSync).toHaveBeenCalledWith(
      expect.stringContaining('.mcp.json.'),
      expect.any(String),
      { mode: 0o600 },
    );
    expect(fsMock.renameSync).toHaveBeenCalledWith(expect.stringContaining('.mcp.json.'), file);
    expect(() => saved()).not.toThrow();
    expect((saved() as { servers: Record<string, unknown> }).servers.local).toBeTruthy();
  });

  it('removeServer prunes repoEnabled references', () => {
    seed({
      servers: {
        a: { type: 'local', command: 'a' },
        b: { type: 'local', command: 'b' },
      },
      repoEnabled: {
        repo1: ['a', 'b'],
        repo2: ['a'],
      },
    });

    const catalog = removeServer('a');

    expect(catalog.servers).toEqual({
      b: { type: 'local', command: 'b', tools: ['*'], timeout: 0 },
    });
    expect(catalog.repoEnabled).toEqual({ repo1: ['b'] });
  });

  it('setRepoEnabled toggles a server for a repo', () => {
    seed({
      servers: {
        a: { type: 'local', command: 'a' },
      },
      repoEnabled: {},
    });

    expect(setRepoEnabled('repo-key', 'a', true).repoEnabled).toEqual({ 'repo-key': ['a'] });
    expect(setRepoEnabled('repo-key', 'a', true).repoEnabled).toEqual({ 'repo-key': ['a'] });
    expect(setRepoEnabled('repo-key', 'a', false).repoEnabled).toEqual({});
  });

  it('returns empty for corrupt files', () => {
    fsMock.files.set(file, '{not valid json');

    expect(readCatalog()).toEqual({ servers: {}, repoEnabled: {} });
  });
});
