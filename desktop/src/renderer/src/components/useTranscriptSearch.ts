import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

// CSS Custom Highlight API registry names. Keep these stable because styles.css
// owns the ::highlight(...) selectors; isolation is handled by per-hook owners.
const HL_ALL = 'chat-search';
const HL_CURRENT = 'chat-search-current';
let nextHighlightOwner = 1;
const highlightOwners = new Map<number, { ranges: Range[]; activeIndex: number }>();
let currentHighlightOwner: number | null = null;

// The CSS Custom Highlight API is available in Electron's Chromium but not in
// jsdom, so every use is feature-detected and the matching logic stays pure.
function highlightApiAvailable(): boolean {
  return (
    typeof Highlight !== 'undefined' && typeof CSS !== 'undefined' && 'highlights' in CSS
  );
}

/**
 * Find every case-insensitive occurrence of `query` in the text nodes under
 * `root`, returned as DOM Ranges in document order. Pure and jsdom-friendly
 * (TreeWalker + Range exist there), so it is unit-testable without the Highlight
 * API or layout.
 */
export function findMatchRanges(root: HTMLElement, query: string): Range[] {
  const needle = query.toLowerCase();
  if (!needle) return [];
  const ranges: Range[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let node = walker.nextNode();
  while (node) {
    const haystack = (node.nodeValue ?? '').toLowerCase();
    let from = 0;
    for (;;) {
      const idx = haystack.indexOf(needle, from);
      if (idx === -1) break;
      const range = document.createRange();
      range.setStart(node, idx);
      range.setEnd(node, idx + needle.length);
      ranges.push(range);
      from = idx + needle.length;
    }
    node = walker.nextNode();
  }
  return ranges;
}

function syncHighlights(): void {
  if (!highlightApiAvailable()) return;
  const allRanges = Array.from(highlightOwners.values()).flatMap((owner) => owner.ranges);
  if (allRanges.length > 0) CSS.highlights.set(HL_ALL, new Highlight(...allRanges));
  else CSS.highlights.delete(HL_ALL);

  const currentOwner =
    currentHighlightOwner === null ? undefined : highlightOwners.get(currentHighlightOwner);
  const active = currentOwner?.ranges[currentOwner.activeIndex];
  if (active) CSS.highlights.set(HL_CURRENT, new Highlight(active));
  else CSS.highlights.delete(HL_CURRENT);
}

function registerHighlights(ownerId: number, ranges: Range[], activeIndex: number): void {
  highlightOwners.set(ownerId, { ranges, activeIndex });
  currentHighlightOwner = ownerId;
  syncHighlights();
}

function clearHighlights(ownerId: number): void {
  highlightOwners.delete(ownerId);
  if (currentHighlightOwner === ownerId) {
    currentHighlightOwner = highlightOwners.size > 0 ? Array.from(highlightOwners.keys()).at(-1) ?? null : null;
  }
  syncHighlights();
}

function scrollRangeIntoView(range: Range | undefined): void {
  if (!range) return;
  const node = range.startContainer;
  const el = node.nodeType === Node.ELEMENT_NODE ? (node as Element) : node.parentElement;
  // jsdom does not implement scrollIntoView, so guard it (and it is a layout
  // concern irrelevant to tests anyway).
  if (el && typeof el.scrollIntoView === 'function') {
    el.scrollIntoView({ block: 'center', inline: 'nearest' });
  }
}

export type TranscriptSearch = {
  matchCount: number;
  // 1-based position of the active match for display (0 when there are none).
  activeOrdinal: number;
  next: () => void;
  prev: () => void;
};

/**
 * Drives an in-transcript find: recomputes match ranges when the query, the
 * enabled flag, or the transcript revision changes, highlights them via the CSS
 * Custom Highlight API, and scrolls the active match into view. Highlight
 * registration and scrolling are feature-detected, so under jsdom the hook still
 * counts/navigates matches but performs no DOM-engine side effects.
 *
 * `revision` should change on real transcript/content changes and when the chat
 * transcript remounts, so ranges never point at detached DOM nodes.
 */
export function useTranscriptSearch(
 contentRef: RefObject<HTMLElement | null>,
 query: string,
 enabled: boolean,
 revision: unknown,
): TranscriptSearch {
 const [matchCount, setMatchCount] = useState(0);
 const [activeIndex, setActiveIndex] = useState(0);
 const rangesRef = useRef<Range[]>([]);
 const ownerIdRef = useRef<number>(0);
 if (ownerIdRef.current === 0) ownerIdRef.current = nextHighlightOwner++;

  // Recompute ranges + the all-matches highlight, and jump to the first match.
  useEffect(() => {
    const root = contentRef.current;
    if (!enabled || !query || !root) {
      rangesRef.current = [];
      setMatchCount(0);
      setActiveIndex(0);
      clearHighlights(ownerIdRef.current);
      return;
    }
    const ranges = findMatchRanges(root, query);
    rangesRef.current = ranges;
    setMatchCount(ranges.length);
    setActiveIndex(0);
    registerHighlights(ownerIdRef.current, ranges, 0);
    scrollRangeIntoView(ranges[0]);
  }, [contentRef, query, enabled, revision]);

  // Re-highlight + scroll when the active match changes (next/prev).
  useEffect(() => {
    const ranges = rangesRef.current;
    if (ranges.length === 0) return;
    registerHighlights(ownerIdRef.current, ranges, activeIndex);
    scrollRangeIntoView(ranges[activeIndex]);
  }, [activeIndex]);

  // Remove only this hook's ranges; other chat tiles keep their highlights.
  useEffect(() => () => clearHighlights(ownerIdRef.current), []);

  const next = useCallback(() => {
    setActiveIndex((i) => {
      const n = rangesRef.current.length;
      return n === 0 ? 0 : (i + 1) % n;
    });
  }, []);

  const prev = useCallback(() => {
    setActiveIndex((i) => {
      const n = rangesRef.current.length;
      return n === 0 ? 0 : (i - 1 + n) % n;
    });
  }, []);

  return {
    matchCount,
    activeOrdinal: matchCount === 0 ? 0 : activeIndex + 1,
    next,
    prev,
  };
}
