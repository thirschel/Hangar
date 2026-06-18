import { describe, expect, it } from 'vitest';
import { appHandlesWheel, encodeWheelSgr } from '../components/termWheel';

describe('termWheel', () => {
  describe('appHandlesWheel', () => {
    it('is false when no mouse tracking is active', () => {
      expect(appHandlesWheel('none')).toBe(false);
    });

    it('is true for the copilot agent (drag) and other tracking modes', () => {
      expect(appHandlesWheel('drag')).toBe(true);
      expect(appHandlesWheel('vt200')).toBe(true);
      expect(appHandlesWheel('any')).toBe(true);
      expect(appHandlesWheel('x10')).toBe(true);
    });
  });

  describe('encodeWheelSgr', () => {
    it('returns empty when there is no vertical movement', () => {
      expect(encodeWheelSgr(0, 0.5, 0.5, 80, 24)).toBe('');
    });

    it('encodes wheel up as button 64', () => {
      expect(encodeWheelSgr(-1, 0, 0, 80, 24)).toBe('\x1b[<64;1;1M');
    });

    it('encodes wheel down as button 65', () => {
      expect(encodeWheelSgr(1, 0, 0, 80, 24)).toBe('\x1b[<65;1;1M');
    });

    it('maps a fractional pointer position to a 1-based cell', () => {
      // 0.5 * 80 => floor(40)+1 = 41 ; 0.5 * 24 => floor(12)+1 = 13
      expect(encodeWheelSgr(-1, 0.5, 0.5, 80, 24)).toBe('\x1b[<64;41;13M');
    });

    it('clamps out-of-range pointer fractions to the terminal bounds', () => {
      expect(encodeWheelSgr(1, 1.5, 1.5, 80, 24)).toBe('\x1b[<65;80;24M');
      expect(encodeWheelSgr(1, -1, -1, 80, 24)).toBe('\x1b[<65;1;1M');
    });

    it('treats non-finite fractions as the top-left cell', () => {
      expect(encodeWheelSgr(-1, Number.NaN, Number.NaN, 80, 24)).toBe('\x1b[<64;1;1M');
    });
  });
});
