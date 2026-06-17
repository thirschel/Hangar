// Defensive guard for a broken experimental Web Storage global in the Node
// dev/server runtime. Node 22+ can expose a global `localStorage` (e.g. when
// launched with `--localstorage-file`). If that global is present but
// non-functional, Next.js' server render touches it and crashes the page with
// "localStorage.getItem is not a function". We remove a broken global so `next
// dev` renders correctly. This runs only in the Node server runtime — it is a
// no-op in a normal Node (where `localStorage` is undefined) and in the browser,
// and does not affect the static export output.
export function register(): void {
  try {
    const store = (globalThis as { localStorage?: { getItem?: unknown } }).localStorage;
    if (store && typeof store.getItem !== "function") {
      Reflect.deleteProperty(globalThis, "localStorage");
      if ((globalThis as { localStorage?: unknown }).localStorage) {
        Object.defineProperty(globalThis, "localStorage", {
          value: undefined,
          configurable: true,
          writable: true,
        });
      }
    }
  } catch {
    // Ignore — a missing or read-only global is fine; the browser is unaffected.
  }
}
