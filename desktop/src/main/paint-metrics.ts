// Pure, electron-free helpers for summarizing a captured bitmap, so the blank-
// terminal diagnostics can be unit-tested without booting Electron or capturing a
// real frame. The decisive question they answer: did the page actually RASTER the
// terminal region (non-background pixels present) even though the user reports a
// blank pane? If yes, the raster happened but was never PRESENTED to the OS window
// (H1, native-present/occlusion); if the region is uniformly background, raster
// never happened (H2, in-renderer compositor).

export type BitmapStats = {
  // Pixels actually examined (after any stride sampling).
  sampled: number;
  // Of those, how many differ from the background colour beyond `tolerance`.
  nonBackground: number;
  // Fraction nonBackground/sampled in [0,1], rounded to 4 dp.
  nonBackgroundRatio: number;
  // Cheap FNV-1a hash of the sampled channel bytes; changes when the picture
  // changes, so two captures can be compared without logging raw pixels.
  hash: string;
};

// analyzeBitmap inspects a BGRA (Electron NativeImage.toBitmap) or RGBA buffer.
// Channel order does not matter for the background comparison because the
// background is grey (R≈G≈B). `stride` samples every Nth pixel for speed on large
// regions (default 1 = every pixel). `bg` defaults to #1e1e1e (the terminal/app
// background). `tolerance` is the per-channel delta under which a pixel counts as
// background.
export function analyzeBitmap(
  bitmap: Uint8Array | null | undefined,
  bg: { r: number; g: number; b: number } = { r: 0x1e, g: 0x1e, b: 0x1e },
  tolerance = 6,
  stride = 1,
): BitmapStats {
  const empty: BitmapStats = { sampled: 0, nonBackground: 0, nonBackgroundRatio: 0, hash: '0' };
  if (!bitmap || bitmap.length < 4) return empty;
  const step = Math.max(1, Math.floor(stride)) * 4;
  let sampled = 0;
  let nonBackground = 0;
  // FNV-1a 32-bit.
  let hash = 0x811c9dc5;
  for (let i = 0; i + 3 < bitmap.length; i += step) {
    const c0 = bitmap[i];
    const c1 = bitmap[i + 1];
    const c2 = bitmap[i + 2];
    sampled += 1;
    // Compare against background irrespective of channel order (grey bg).
    const isBg =
      Math.abs(c0 - bg.b) <= tolerance &&
      Math.abs(c1 - bg.g) <= tolerance &&
      Math.abs(c2 - bg.r) <= tolerance;
    if (!isBg) nonBackground += 1;
    hash ^= c0;
    hash = Math.imul(hash, 0x01000193);
    hash ^= c1;
    hash = Math.imul(hash, 0x01000193);
    hash ^= c2;
    hash = Math.imul(hash, 0x01000193);
  }
  const ratio = sampled > 0 ? Math.round((nonBackground / sampled) * 1e4) / 1e4 : 0;
  return {
    sampled,
    nonBackground,
    nonBackgroundRatio: ratio,
    hash: (hash >>> 0).toString(16),
  };
}
