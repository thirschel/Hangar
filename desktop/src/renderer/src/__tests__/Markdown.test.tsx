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

  it('wraps each word in a .word-fade span when animate is set', () => {
    const { container } = render(<Markdown text="Hello there world" animate />);
    const words = container.querySelectorAll('.word-fade');
    // One span per word (trailing whitespace kept on the word).
    expect(words).toHaveLength(3);
    // The spans tile the text without dropping any characters.
    expect(
      Array.from(words)
        .map((word) => word.textContent)
        .join(''),
    ).toBe('Hello there world');
  });

  it('does not split a fenced code block into word spans even when animate is set', () => {
    const md = ['```ts', 'const answer = 42;', '```'].join('\n');
    const { container } = render(<Markdown text={md} animate />);

    const pre = container.querySelector('pre.md-code');
    expect(pre).not.toBeNull();
    // Code text stays verbatim -- no per-word spans inside the block.
    expect(pre?.querySelectorAll('.word-fade')).toHaveLength(0);
    expect(pre?.textContent).toContain('const answer = 42;');
  });

  it('does not split inline code into word spans when animate is set', () => {
    const { container } = render(<Markdown text="Run `npm install` now" animate />);
    const code = container.querySelector('code.md-inline-code');
    expect(code).not.toBeNull();
    expect(code?.querySelectorAll('.word-fade')).toHaveLength(0);
    expect(code?.textContent).toBe('npm install');
    // Surrounding prose is still word-wrapped.
    expect(container.querySelectorAll('.word-fade').length).toBeGreaterThan(0);
  });

  it('renders no .word-fade spans without animate (default)', () => {
    const { container } = render(<Markdown text="Hello there world" />);
    expect(container.querySelectorAll('.word-fade')).toHaveLength(0);
    // The unsplit text is a single node, queryable as a whole.
    expect(screen.getByText('Hello there world')).toBeInTheDocument();
  });
});
