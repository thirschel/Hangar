import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { log } from './logger';
import { csDir } from './settings';

export type McpServerDef = {
  type: 'local' | 'http';
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  cwd?: string;
  url?: string;
  headers?: Record<string, string>;
  tools: string[];
  timeout: number;
};

export type McpCatalog = {
  servers: Record<string, McpServerDef>;
  repoEnabled: Record<string, string[]>;
};

const EMPTY_CATALOG: McpCatalog = { servers: {}, repoEnabled: {} };

export function mcpJsonPath(): string {
  return path.join(csDir(), 'mcp.json');
}

function isObject(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function stringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  return value.filter((item): item is string => typeof item === 'string');
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (!isObject(value)) return undefined;
  const result: Record<string, string> = {};
  for (const [key, val] of Object.entries(value)) {
    if (typeof val === 'string') result[key] = val;
  }
  return result;
}

function coerceTimeout(value: unknown): number {
  const n = Math.trunc(Number(value));
  if (!Number.isFinite(n)) return 0;
  return Math.min(600, Math.max(0, n));
}

function normalizeServerDef(def: unknown): McpServerDef {
  if (!isObject(def)) throw new Error('MCP server definition must be an object');

  const rawType = def.type === 'sse' ? 'http' : def.type;
  if (rawType !== 'local' && rawType !== 'http') {
    throw new Error("MCP server type must be 'local' or 'http'");
  }

  const tools = stringArray(def.tools);
  const normalized: McpServerDef = {
    type: rawType,
    tools: tools && tools.length > 0 ? tools : ['*'],
    timeout: coerceTimeout(def.timeout),
  };

  if (rawType === 'local') {
    if (typeof def.command !== 'string' || def.command.trim() === '') {
      throw new Error('Local MCP server requires command');
    }
    normalized.command = def.command;
    const args = stringArray(def.args);
    if (args) normalized.args = args;
    const env = stringRecord(def.env);
    if (env) normalized.env = env;
    if (typeof def.cwd === 'string') normalized.cwd = def.cwd;
  } else {
    if (typeof def.url !== 'string' || def.url.trim() === '') {
      throw new Error('HTTP MCP server requires url');
    }
    normalized.url = def.url;
    const headers = stringRecord(def.headers);
    if (headers) normalized.headers = headers;
  }

  return normalized;
}

function normalizeName(name: unknown): string {
  if (typeof name !== 'string') throw new Error('MCP server name must be a string');
  const trimmed = name.trim();
  if (!trimmed) throw new Error('MCP server name must be non-empty');
  return trimmed;
}

function normalizeCatalog(raw: unknown, strictServers: boolean): McpCatalog {
  if (!isObject(raw)) return { ...EMPTY_CATALOG };

  const servers: Record<string, McpServerDef> = {};
  if (isObject(raw.servers)) {
    for (const [rawName, rawDef] of Object.entries(raw.servers)) {
      try {
        const name = normalizeName(rawName);
        if (Object.prototype.hasOwnProperty.call(servers, name)) {
          throw new Error(`Duplicate MCP server name: ${name}`);
        }
        servers[name] = normalizeServerDef(rawDef);
      } catch (error) {
        if (strictServers) throw error;
        log.error('Ignoring invalid MCP server definition', rawName, error);
      }
    }
  }

  const repoEnabled: Record<string, string[]> = {};
  if (isObject(raw.repoEnabled)) {
    for (const [repoKey, rawNames] of Object.entries(raw.repoEnabled)) {
      if (typeof repoKey !== 'string' || !repoKey) continue;
      const names = stringArray(rawNames)?.filter((name) =>
        Object.prototype.hasOwnProperty.call(servers, name),
      );
      if (names && names.length > 0) repoEnabled[repoKey] = Array.from(new Set(names));
    }
  }

  return { servers, repoEnabled };
}

export function readCatalog(): McpCatalog {
  try {
    return normalizeCatalog(JSON.parse(readFileSync(mcpJsonPath(), 'utf8')) as unknown, false);
  } catch (error) {
    const code = isObject(error) ? error.code : undefined;
    if (code !== 'ENOENT') log.error('Failed to read MCP catalog', error);
    return { servers: {}, repoEnabled: {} };
  }
}

function writeCatalog(catalog: McpCatalog): McpCatalog {
  const normalized = normalizeCatalog(catalog, true);
  mkdirSync(csDir(), { recursive: true });
  const file = mcpJsonPath();
  const tmp = path.join(csDir(), `.mcp.json.${process.pid}.${Date.now()}.tmp`);
  const json = `${JSON.stringify(normalized, null, 2)}\n`;
  // 0o600 is best-effort on NTFS: Node ignores POSIX bits on Windows. Real
  // hardening would require a DACL/icacls pass, which is out of scope here.
  writeFileSync(tmp, json, { mode: 0o600 });
  renameSync(tmp, file);
  return normalized;
}

function readCatalogForWrite(): McpCatalog {
  return readCatalog();
}

export function upsertServer(name: string, def: unknown): McpCatalog {
  const normalizedName = normalizeName(name);
  const catalog = readCatalogForWrite();
  if (
    name !== normalizedName &&
    Object.prototype.hasOwnProperty.call(catalog.servers, normalizedName)
  ) {
    throw new Error(`Duplicate MCP server name: ${normalizedName}`);
  }
  for (const existing of Object.keys(catalog.servers)) {
    if (existing !== normalizedName && existing.trim() === normalizedName) {
      throw new Error(`Duplicate MCP server name: ${normalizedName}`);
    }
  }
  catalog.servers[normalizedName] = normalizeServerDef(def);
  return writeCatalog(catalog);
}

export function removeServer(name: string): McpCatalog {
  const normalizedName = normalizeName(name);
  const catalog = readCatalogForWrite();
  delete catalog.servers[normalizedName];
  for (const [repoKey, names] of Object.entries(catalog.repoEnabled)) {
    const remaining = names.filter((enabledName) => enabledName !== normalizedName);
    if (remaining.length > 0) catalog.repoEnabled[repoKey] = remaining;
    else delete catalog.repoEnabled[repoKey];
  }
  return writeCatalog(catalog);
}

export function setRepoEnabled(repoKey: string, name: string, on: boolean): McpCatalog {
  if (typeof repoKey !== 'string' || repoKey.trim() === '') {
    throw new Error('MCP repoKey must be non-empty');
  }
  const normalizedName = normalizeName(name);
  const catalog = readCatalogForWrite();
  if (!Object.prototype.hasOwnProperty.call(catalog.servers, normalizedName)) {
    throw new Error(`Unknown MCP server: ${normalizedName}`);
  }
  const current = catalog.repoEnabled[repoKey] ?? [];
  if (on) {
    catalog.repoEnabled[repoKey] = Array.from(new Set([...current, normalizedName]));
  } else {
    const remaining = current.filter((enabledName) => enabledName !== normalizedName);
    if (remaining.length > 0) catalog.repoEnabled[repoKey] = remaining;
    else delete catalog.repoEnabled[repoKey];
  }
  return writeCatalog(catalog);
}
