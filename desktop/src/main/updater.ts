import { app } from 'electron';
import { autoUpdater } from 'electron-updater';
import { log } from './logger';

// Wires up electron-updater. Only runs in packaged builds (there is nothing to
// update in dev), and every failure is logged rather than thrown so a missing
// release / offline machine can never crash the app.
export function initAutoUpdate(): void {
  if (!app.isPackaged) return;
  try {
    autoUpdater.logger = {
      info: (...a: unknown[]) => log.info('updater', ...a),
      warn: (...a: unknown[]) => log.info('updater:warn', ...a),
      error: (...a: unknown[]) => log.error('updater', ...a),
      debug: () => {},
    } as never;
    autoUpdater.autoDownload = true;
    autoUpdater.on('error', (err) => log.error('autoUpdater error', err));
    autoUpdater.on('update-available', (info) => log.info('update available', info?.version));
    autoUpdater.on('update-downloaded', (info) => log.info('update downloaded', info?.version));
    void autoUpdater.checkForUpdatesAndNotify().catch((err) => log.error('checkForUpdates', err));
  } catch (err) {
    log.error('initAutoUpdate', err);
  }
}
