/// <reference types="vite/client" />

import type { CsApi } from '../preload';

declare global {
  interface Window {
    cs: CsApi;
  }
}
