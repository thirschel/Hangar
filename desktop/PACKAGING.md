# Packaging & distribution (E6)

The Hangar desktop app is packaged with [electron-builder] into a Windows **NSIS**
installer that bundles the Go core-daemon (`cs.exe`).

## Prerequisites

1. Build the core-daemon first so it exists at the repo root `dist\cs.exe`
   (the installer ships this verbatim — see `electron-builder.yml` → `extraResources`):

   ```pwsh
   # from the repo root (for example D:\dev\Hangar)
   go build -o dist\cs.exe .
   ```

2. (Re)generate the icons if you change `scripts/make-icons.js`:

   ```pwsh
   npm run make-icons   # writes build/icon.png (256) and build/tray.png (32)
   ```

## Build the app

```pwsh
cd desktop
npm install
npm run pack    # unpacked app in release/win-unpacked (fast smoke test)
npm run dist    # full NSIS installer in release/Hangar-Setup-<version>.exe
```

At runtime the packaged app finds the daemon at
`process.resourcesPath\dist\cs.exe` and the tray icon at
`process.resourcesPath\build\tray.png` (`src/main/assets.ts`).

## Auto-update

`electron-updater` is wired in `src/main/updater.ts` and runs **only in packaged
builds**. The release feed is configured in `electron-builder.yml` → `publish`
(GitHub provider) and should point at `thirschel/Hangar`. Publish with
`electron-builder --win nsis --publish always` (needs a `GH_TOKEN`).
If no release exists the updater logs and no-ops — it never crashes the app.

## Code signing (currently UNSIGNED)

No certificate is configured, so the installer is **unsigned** and Windows
SmartScreen will warn on first run. To sign:

- **EV / OV certificate (file):** set `CSC_LINK` (path or base64 of the `.pfx`) and
  `CSC_KEY_PASSWORD` env vars before `npm run dist`; electron-builder signs
  automatically.
- **Azure Trusted Signing / cloud HSM:** configure `win.azureSignOptions` (or a
  custom `sign` hook) in `electron-builder.yml`.

EV certificates remove the SmartScreen warning immediately; OV builds reputation
over time. See the electron-builder code-signing docs for details.

## Logs

The main process writes to `~/.hangar/desktop.log` (`src/main/logger.ts`),
including auto-updater activity and any uncaught exceptions.

[electron-builder]: https://www.electron.build/
