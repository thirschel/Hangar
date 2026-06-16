import { appendFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';

const logFile = path.join(os.homedir(), '.claude-squad', 'desktop.log');

function write(level: string, args: unknown[]): void {
  try {
    mkdirSync(path.dirname(logFile), { recursive: true });
    const text = args
      .map((a) => (a instanceof Error ? a.stack || a.message : typeof a === 'string' ? a : JSON.stringify(a)))
      .join(' ');
    appendFileSync(logFile, `${new Date().toISOString()} [${level}] ${text}\n`);
  } catch {
    // Never let logging crash the app.
  }
}

export const log = {
  file: logFile,
  info: (...args: unknown[]): void => {
    write('INFO', args);
    console.log(...args);
  },
  error: (...args: unknown[]): void => {
    write('ERROR', args);
    console.error(...args);
  },
};
