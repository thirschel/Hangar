// Electron module mock for Vitest — prevents renderer tests from importing the
// real Electron bindings (which require a running Electron process).
export const app = {
  isPackaged: false,
  getAppPath: () => '/mock/app',
  quit: () => {},
  whenReady: () => Promise.resolve(),
  on: () => {},
  setBadgeCount: () => {},
};

export const BrowserWindow = class {
  constructor() {}
  loadURL() {}
  loadFile() {}
  on() {}
  show() {}
  hide() {}
  focus() {}
  isMinimized() { return false; }
  isVisible() { return true; }
  isDestroyed() { return false; }
  restore() {}
  webContents = { send: () => {}, once: () => {} };
};

export const ipcMain = {
  handle: () => {},
  on: () => {},
};

export const ipcRenderer = {
  invoke: async () => ({}),
  on: () => {},
  send: () => {},
  removeListener: () => {},
};

export const contextBridge = {
  exposeInMainWorld: () => {},
};

export const dialog = {
  showOpenDialog: async () => ({ canceled: true, filePaths: [] }),
};

export const shell = {
  openExternal: async () => {},
};

export const globalShortcut = {
  register: () => {},
  unregisterAll: () => {},
};

export const Notification = class {
  static isSupported() { return false; }
  constructor() {}
  on() { return this; }
  show() {}
};

export const Menu = {
  buildFromTemplate: () => ({}),
};

export const Tray = class {
  constructor() {}
  setToolTip() {}
  setContextMenu() {}
  on() {}
  destroy() {}
};

export const nativeImage = {
  createFromPath: () => ({ isEmpty: () => true }),
  createEmpty: () => ({}),
};

export const autoUpdater = {
  logger: null,
  autoDownload: false,
  on: () => {},
  checkForUpdatesAndNotify: async () => {},
};
