import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

type BreadcrumbCopyProps = {
  label: string;
  path: string;
  className?: string;
  tipAriaLabel?: string;
};

const COPIED_TEXT = 'Copied to clipboard';
const COPIED_DWELL_MS = 1000;

// BreadcrumbCopy renders a single clickable breadcrumb segment (repo name or
// branch). Hovering/focusing it reveals a tooltip below showing `path`; clicking
// (or Enter/Space) copies `path` to the clipboard, swaps the tooltip text to
// "Copied to clipboard", then fades it out. After a copy the tooltip stays hidden
// until the pointer leaves and re-enters, so it doesn't immediately re-show the
// path while the segment is still hovered.
export function BreadcrumbCopy({
  label,
  path,
  className,
  tipAriaLabel,
}: BreadcrumbCopyProps): JSX.Element {
  const [visible, setVisible] = useState(false);
  const [copied, setCopied] = useState(false);
  const [tipWidth, setTipWidth] = useState<number | undefined>(undefined);

  // Don't re-show the path tooltip until the pointer leaves and re-enters.
  const suppressedRef = useRef(false);
  const fadeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const labelRef = useRef<HTMLSpanElement>(null);

  const clearFadeTimer = useCallback((): void => {
    if (fadeTimer.current !== null) {
      clearTimeout(fadeTimer.current);
      fadeTimer.current = null;
    }
  }, []);

  // Cancel any pending fade timer on unmount.
  useEffect(() => clearFadeTimer, [clearFadeTimer]);

  const text = copied ? COPIED_TEXT : path;

  // Measure the rendered tooltip text while visible so the width change between
  // the path and "Copied to clipboard" animates via the CSS width transition.
  // When hidden we drop back to auto width so the next reveal starts at the
  // natural size instead of animating from a stale value. The tooltip is
  // absolutely positioned, so this never reflows the header.
  useLayoutEffect(() => {
    if (visible && labelRef.current) {
      setTipWidth(labelRef.current.offsetWidth);
    } else if (!visible) {
      setTipWidth(undefined);
    }
  }, [text, visible]);

  const show = useCallback((): void => {
    if (suppressedRef.current) return;
    clearFadeTimer();
    setCopied(false);
    setVisible(true);
  }, [clearFadeTimer]);

  const hide = useCallback((): void => {
    clearFadeTimer();
    suppressedRef.current = false;
    setVisible(false);
  }, [clearFadeTimer]);

  const copy = useCallback((): void => {
    void navigator.clipboard.writeText(path);
    clearFadeTimer();
    setCopied(true);
    setVisible(true);
    fadeTimer.current = setTimeout(() => {
      suppressedRef.current = true;
      setVisible(false);
      fadeTimer.current = null;
    }, COPIED_DWELL_MS);
  }, [path, clearFadeTimer]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent): void => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        copy();
      }
    },
    [copy],
  );

  return (
    <span
      className={`breadcrumb__item${className ? ` ${className}` : ''}`}
      role="button"
      tabIndex={0}
      aria-label={tipAriaLabel}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onClick={copy}
      onKeyDown={onKeyDown}
    >
      {label}
      <span
        className={`breadcrumb__tip${visible ? ' is-visible' : ''}${
          copied ? ' breadcrumb__tip--copied' : ''
        }`}
        role="tooltip"
        aria-hidden={!visible}
        style={visible && tipWidth !== undefined ? { width: tipWidth } : undefined}
      >
        <span className="breadcrumb__tip-label" ref={labelRef}>
          {text}
        </span>
      </span>
    </span>
  );
}
