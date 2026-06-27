// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { RefObject } from 'react';
import { findMatchRanges, useTranscriptSearch } from '../useTranscriptSearch';

function mountHost(html: string): HTMLElement {
  const host = document.createElement('div');
  host.innerHTML = html;
  document.body.appendChild(host);
  return host;
}

afterEach(() => {
  document.body.innerHTML = '';
});

describe('findMatchRanges', () => {
  it('finds every case-insensitive occurrence across text nodes, in order', () => {
    const host = mountHost('<p>Foo foo</p><div>bar</div><p>FOO</p>');
    const ranges = findMatchRanges(host, 'foo');
    expect(ranges).toHaveLength(3);
    expect(ranges.map((r) => r.toString().toLowerCase())).toEqual(['foo', 'foo', 'foo']);
  });

  it('returns nothing for an empty query', () => {
    const host = mountHost('<p>anything</p>');
    expect(findMatchRanges(host, '')).toHaveLength(0);
  });

  it('finds repeated matches within a single text node', () => {
    const host = mountHost('<p>ababab</p>');
    expect(findMatchRanges(host, 'ab')).toHaveLength(3);
  });
});

describe('useTranscriptSearch', () => {
  function renderSearch(host: HTMLElement, query: string, enabled = true, revision = 0) {
    const ref = { current: host } as RefObject<HTMLElement>;
    return renderHook(
      ({ q, en, rev }) => useTranscriptSearch(ref, q, en, rev),
      { initialProps: { q: query, en: enabled, rev: revision } },
    );
  }

  it('counts matches and reports a 1-based active ordinal', () => {
    const host = mountHost('<p>alpha beta alpha</p><p>alpha</p>');
    const { result } = renderSearch(host, 'alpha');
    expect(result.current.matchCount).toBe(3);
    expect(result.current.activeOrdinal).toBe(1);
  });

  it('cycles forward/backward with wrap-around', () => {
    const host = mountHost('<p>x x x</p>');
    const { result } = renderSearch(host, 'x');
    expect(result.current.matchCount).toBe(3);

    act(() => result.current.next());
    expect(result.current.activeOrdinal).toBe(2);
    act(() => result.current.next());
    expect(result.current.activeOrdinal).toBe(3);
    act(() => result.current.next()); // wraps to first
    expect(result.current.activeOrdinal).toBe(1);
    act(() => result.current.prev()); // wraps to last
    expect(result.current.activeOrdinal).toBe(3);
  });

  it('reports no matches when disabled or empty', () => {
    const host = mountHost('<p>hello hello</p>');
    const { result, rerender } = renderSearch(host, 'hello', false);
    expect(result.current.matchCount).toBe(0);
    expect(result.current.activeOrdinal).toBe(0);

    rerender({ q: 'hello', en: true, rev: 0 });
    expect(result.current.matchCount).toBe(2);

    rerender({ q: '', en: true, rev: 0 });
    expect(result.current.matchCount).toBe(0);
    expect(result.current.activeOrdinal).toBe(0);
  });
});
