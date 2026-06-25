// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Markdown } from '../components/Markdown';

describe('Markdown', () => {
  it('renders a paragraph as a <p>', () => {
    render(<Markdown text="Hello world" />);
    const para = screen.getByText('Hello world');
    expect(para).toBeInTheDocument();
    expect(para.tagName).toBe('P');
  });

  it('renders a fenced ts code block as highlighted code inside a pre/code', () => {
    const md = ['```ts', 'const answer: number = 42;', '```'].join('\n');
    const { container } = render(<Markdown text={md} />);

    const pre = container.querySelector('pre.md-code');
    expect(pre).not.toBeNull();
    expect(pre).toHaveAttribute('data-language', 'ts');

    // The code text lives inside a <code> nested in the <pre>.
    const code = pre?.querySelector('code');
    expect(code).not.toBeNull();
    // Tokens are split across spans, so assert on the combined text content.
    expect(code?.textContent).toContain('const answer: number = 42;');

    // Prism emitted coloured token spans -> the block is syntax highlighted.
    const coloured = Array.from(pre?.querySelectorAll('span') ?? []).filter((span) =>
      (span.getAttribute('style') ?? '').includes('color'),
    );
    expect(coloured.length).toBeGreaterThan(0);
  });

  it('renders a GFM table wrapped for horizontal scroll', () => {
    const md = ['| Name | Role |', '| ---- | ---- |', '| Ada  | Dev  |'].join('\n');
    const { container } = render(<Markdown text={md} />);

    const table = container.querySelector('table.md-table');
    expect(table).not.toBeNull();
    expect(table?.tagName).toBe('TABLE');
    expect(table?.closest('.md-table-wrap')).not.toBeNull();
    expect(screen.getByText('Name')).toBeInTheDocument();
    expect(screen.getByText('Ada')).toBeInTheDocument();
  });

  it('renders inline code as a <code>', () => {
    render(<Markdown text="Run `npm install` now" />);
    const code = screen.getByText('npm install');
    expect(code.tagName).toBe('CODE');
    expect(code).toHaveClass('md-inline-code');
  });

  it('opens links in a new tab without leaking the opener', () => {
    render(<Markdown text="[Hangar](https://example.com)" />);
    const link = screen.getByRole('link', { name: 'Hangar' });
    expect(link).toHaveAttribute('href', 'https://example.com');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noreferrer noopener');
  });

  it('does not render raw HTML as elements (escaped, no injection)', () => {
    const { container } = render(
      <Markdown text={'<img src=x onerror="alert(1)"> and <b>bold</b> text'} />,
    );

    // The raw HTML must never become live elements.
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('b')).toBeNull();
    expect(container.querySelector('[onerror]')).toBeNull();

    // It is escaped and surfaced as literal text instead.
    expect(container.textContent).toContain('onerror');
  });
});
