import { describe, expect, it } from 'vitest';
import { reorder } from '../grid-reorder';

describe('reorder', () => {
  it('moves a tile forward to land after the target', () => {
    expect(reorder(['a', 'b', 'c', 'd'], 'a', 'c')).toEqual(['b', 'c', 'a', 'd']);
  });

  it('moves a tile backward to land before the target', () => {
    expect(reorder(['a', 'b', 'c', 'd'], 'd', 'b')).toEqual(['a', 'd', 'b', 'c']);
  });

  it('handles adjacent forward moves', () => {
    expect(reorder(['a', 'b', 'c'], 'a', 'b')).toEqual(['b', 'a', 'c']);
  });

  it('handles adjacent backward moves', () => {
    expect(reorder(['a', 'b', 'c'], 'c', 'b')).toEqual(['a', 'c', 'b']);
  });

  it('returns an unchanged copy when dragged === target', () => {
    const input = ['a', 'b', 'c'];
    const out = reorder(input, 'b', 'b');
    expect(out).toEqual(['a', 'b', 'c']);
    expect(out).not.toBe(input);
  });

  it('returns an unchanged copy when an id is unknown', () => {
    expect(reorder(['a', 'b', 'c'], 'x', 'b')).toEqual(['a', 'b', 'c']);
    expect(reorder(['a', 'b', 'c'], 'a', 'z')).toEqual(['a', 'b', 'c']);
  });

  it('does not mutate the input array', () => {
    const input = ['a', 'b', 'c', 'd'];
    reorder(input, 'a', 'd');
    expect(input).toEqual(['a', 'b', 'c', 'd']);
  });
});
