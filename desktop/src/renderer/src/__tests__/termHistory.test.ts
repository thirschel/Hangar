import { describe, expect, it } from 'vitest';
import { isAtBottom, normalizeHistory, shouldReconcile } from '../components/termHistory';

describe('termHistory', () => {
  describe('isAtBottom', () => {
    it('returns true at or past the bottom', () => {
      expect(isAtBottom(10, 10)).toBe(true);
      expect(isAtBottom(11, 10)).toBe(true);
    });

    it('returns false above the bottom', () => {
      expect(isAtBottom(9, 10)).toBe(false);
    });
  });

  describe('shouldReconcile', () => {
    it('reconciles when reviewing history and host scrollback grew', () => {
      expect(shouldReconcile(10, 11, false)).toBe(true);
    });

    it('does not reconcile at the bottom', () => {
      expect(shouldReconcile(10, 11, true)).toBe(false);
    });

    it('does not reconcile without growth', () => {
      expect(shouldReconcile(10, 10, false)).toBe(false);
      expect(shouldReconcile(10, 9, false)).toBe(false);
    });
  });

  describe('normalizeHistory', () => {
    it('returns empty input unchanged', () => {
      expect(normalizeHistory('')).toBe('');
    });

    it('adds one trailing newline when missing', () => {
      expect(normalizeHistory('hello')).toBe('hello\n');
    });

    it('collapses existing trailing newlines to one newline', () => {
      expect(normalizeHistory('hello\n\n')).toBe('hello\n');
    });

    it('normalizes trailing CRLF newlines', () => {
      expect(normalizeHistory('hello\r\n\r\n')).toBe('hello\n');
    });
  });
});
