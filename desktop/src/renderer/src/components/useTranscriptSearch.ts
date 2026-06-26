import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

// CSS Custom Highlight API registry names. Two highlights: every match (subtle)
// and the active match (strong). They are document-global, but only the focused
// chat's search populates them at any time (others contribute no ranges), so the
// highlighting is effectively scoped to the active transcript.
const HL_ALL = 'chat-search';
const HL_CURRENT = 'chat-search-current';

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

function registerHighlights(ranges: Range[], activeIndex: number): void {
  if (!highlightApiAvailable()) return;
  CSS.highlights.set(HL_ALL, new Highlight(...ranges));
  const active = ranges[activeIndex];
  if (active) CSS.highlights.set(HL_CURRENT, new Highlight(active));
  else CSS.highlights.delete(HL_CURRENT);
}

function clearHighlights(): void {
  if (!highlightApiAvailable()) return;
  CSS.highlights.delete(HL_ALL);
  CSS.highlights.delete(HL_CURRENT);
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
 * `revision` should change on structural transcript changes (e.g. entry count)
 * so matches refresh as new messages arrive, without resetting the user's
 * position on every streaming reveal tick.
 */
export function useTranscriptSearch(
  contentRef: RefObject<HTMLElement | null>,
  query: string,
  enabled: boolean,
  revision: number,
): TranscriptSearch {
  const [matchCount, setMatchCount] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  const rangesRef = useRef<Range[]>([]);

  // Recompute ranges + the all-matches highlight, and jump to the first match.
  useEffect(() => {
    const root = contentRef.current;
    if (!enabled || !query || !root) {
      rangesRef.current = [];
      setMatchCount(0);
      setActiveIndex(0);
      clearHighlights();
      return;
    }
    const ranges = findMatchRanges(root, query);
    rangesRef.current = ranges;
    setMatchCount(ranges.length);
    setActiveIndex(0);
    registerHighlights(ranges, 0);
    scrollRangeIntoView(ranges[0]);
  }, [contentRef, query, enabled, revision]);

  // Re-highlight + scroll when the active match changes (next/prev).
  useEffect(() => {
    const ranges = rangesRef.current;
    if (ranges.length === 0) return;
    registerHighlights(ranges, activeIndex);
    scrollRangeIntoView(ranges[activeIndex]);
  }, [activeIndex]);

  // Always clear the global highlights when the chat unmounts.
  useEffect(() => () => clearHighlights(), []);

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
