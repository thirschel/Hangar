import { app, Menu, Tray, nativeImage, type BrowserWindow } from 'electron';
import { buildAsset } from './assets';

let tray: Tray | null = null;

// Reveals and focuses the main window (restoring it if minimized/hidden).
function showWindow(win: BrowserWindow | null): void {
  if (!win) return;
  if (win.isMinimized()) win.restore();
  win.show();
  win.focus();
}

export function createTray(getWindow: () => BrowserWindow | null): Tray {
  const image = nativeImage.createFromPath(buildAsset('tray.png'));
  tray = new Tray(image.isEmpty() ? nativeImage.createEmpty() : image);
  tray.setToolTip('claude-squad');

  const menu = Menu.buildFromTemplate([
    { label: 'Show claude-squad', click: () => showWindow(getWindow()) },
    { type: 'separator' },
    {
      label: 'Quit',
      click: () => {
        app.quit();
      },
    },
  ]);
  tray.setContextMenu(menu);
  tray.on('click', () => {
    const win = getWindow();
    if (win && win.isVisible() && !win.isMinimized()) {
      win.hide();
    } else {
      showWindow(win);
    }
  });
  return tray;
}

export function destroyTray(): void {
  tray?.destroy();
  tray = null;
}
