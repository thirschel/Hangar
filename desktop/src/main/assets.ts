import { app } from 'electron';
import path from 'node:path';

// Resolves a file in the build/ assets dir for both dev and packaged runs.
// In packaged builds the build/ dir is shipped under resources/ (extraResources).
export function buildAsset(name: string): string {
  return app.isPackaged
    ? path.join(process.resourcesPath, 'build', name)
    : path.join(__dirname, '..', '..', 'build', name);
}
