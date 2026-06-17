// @vitest-environment jsdom
import { render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Modal } from '../components/Modal';
import { MODE_LABELS, SIDEBAR_MODES } from '../components/sidebar-modes';

describe('snapshots', () => {
  it('matches the Modal snapshot', () => {
    const { container } = render(
      <Modal title="Test Modal" onClose={vi.fn()}>
        <p>content</p>
      </Modal>,
    );

    expect(container).toMatchSnapshot();
  });

  it('matches the sidebar modes data snapshot', () => {
    expect({ SIDEBAR_MODES, MODE_LABELS }).toMatchSnapshot();
  });
});
