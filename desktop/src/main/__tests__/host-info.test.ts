import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock the OS boundaries so tryLoadValidHostInfo is deterministic and
// cross-platform: filesystem reads, the `whoami` SID lookup behind
// controlPipeName(), and the powershell process-creation-time probe.
// vi.hoisted lets the (hoisted) vi.mock factories reference these mocks.
const { fsMock, cpMock } = vi.hoisted(() => ({
  fsMock: {
    existsSync: vi.fn<(p: string) => boolean>(),
    readFileSync: vi.fn<() => string>(),
  },
  cpMock: {
    execFileSync: vi.fn<(cmd: string) => string>(),
    spawn: vi.fn(),
  },
}));

vi.mock('node:fs', () => fsMock);
vi.mock('node:child_process', () => cpMock);

import { PROTO_VERSION, tryLoadValidHostInfo } from '../host-client';

const SID = 'S-1-5-21-111-222-333-1000';
const PIPE = `\\\\.\\pipe\\hangar-host-${SID}`;
const NONCE = 'a'.repeat(64);
const CREATED_UNIX = 1700000000;

function hostJson(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    pipeName: PIPE,
    pid: 4242,
    createdUnix: CREATED_UNIX,
    nonce: NONCE,
    version: PROTO_VERSION,
    ...overrides,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  fsMock.existsSync.mockReturnValue(true);
  cpMock.execFileSync.mockImplementation((cmd: string) => {
    // controlPipeName() -> currentUserSid() -> whoami CSV containing the SID.
    if (cmd === 'whoami') return `"machine\\user","${SID}"\n`;
    // processCreationUnix() -> powershell prints the process creation unix time.
    return String(CREATED_UNIX);
  });
});

describe('tryLoadValidHostInfo', () => {
  it('returns null (not throw) when host.json version mismatches the client', () => {
    // The exact stale-daemon scenario: a force-killed v(N-1) daemon left a
    // host.json behind. The new client must treat it as unusable and respawn,
    // not abort startup with "protocol mismatch".
    fsMock.readFileSync.mockReturnValue(hostJson({ version: PROTO_VERSION - 1 }));

    expect(tryLoadValidHostInfo()).toBeNull();
  });

  it('returns null when host.json points at a dead pid (creation-time mismatch)', () => {
    fsMock.readFileSync.mockReturnValue(hostJson({ createdUnix: CREATED_UNIX + 5000 }));

    expect(tryLoadValidHostInfo()).toBeNull();
  });

  it('returns null when host.json is corrupt', () => {
    fsMock.readFileSync.mockReturnValue('{ not valid json');

    expect(tryLoadValidHostInfo()).toBeNull();
  });

  it('returns null when host.json does not exist', () => {
    fsMock.existsSync.mockReturnValue(false);

    expect(tryLoadValidHostInfo()).toBeNull();
  });

  it('returns the HostInfo when host.json is valid and current', () => {
    fsMock.readFileSync.mockReturnValue(hostJson());

    const hi = tryLoadValidHostInfo();

    expect(hi).not.toBeNull();
    expect(hi?.version).toBe(PROTO_VERSION);
    expect(hi?.pid).toBe(4242);
    expect(hi?.pipeName).toBe(PIPE);
  });
});
