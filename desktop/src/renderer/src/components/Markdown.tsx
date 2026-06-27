import { memo } from 'react';
import type { JSX } from 'react';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Highlight, themes } from 'prism-react-renderer';
import type { Element, ElementContent, Root, RootContent, Text } from 'hast';

/**
 * Reusable Markdown renderer for assistant / agent output.
 *
 * Renders CLI-fidelity Markdown -- paragraphs, lists, fenced code blocks with
 * syntax highlighting, inline code, GFM tables, blockquotes and links -- styled
 * to sit cleanly inside a chat transcript (code and tables visually set off in
 * their own blocks).
 *
 * Security: raw HTML is intentionally NOT rendered. We rely on react-markdown's
 * default behaviour (HTML is escaped, never parsed into elements) and
 * deliberately do not enable `rehype-raw` or any other raw-HTML passthrough, so
 * (untrusted-ish) assistant output cannot inject markup or scripts.
 */
type MarkdownProps = {
  text: string;
  // When true, wrap each rendered word in a `.word-fade` span so words fade in
  // one-after-another as streaming deltas grow the text (Claude-app style).
  // React reconciles the already-rendered word spans by position, so only the
  // newly-arrived words mount and animate -- earlier words never re-flash. Leave
  // false/undefined for finalized, resumed or historical messages so they render
  // instantly with no animation.
  animate?: boolean;
};

// GFM tables, strikethrough, task lists and autolinks.
const REMARK_PLUGINS = [remarkGfm];

// Dark, VS Code-flavoured token colours that pair with the app's dark surfaces.
// Only the per-token colours are applied inline; the block background, padding
// and radius come from the `.md-code` rule in styles.css so it matches the rest
// of the UI.
const CODE_THEME = themes.vsDark;

// Matches a fenced code block's language class, e.g. `language-ts`.
const LANGUAGE_CLASS = /language-(\w[\w-]*)/;

// --- Per-word fade-in (streaming) ------------------------------------------
// A rehype transform that splits every text node into per-word spans so the live
// streaming assistant message can fade words in one-after-another. Code is left
// untouched: text inside <pre>/<code> must stay verbatim for syntax rendering,
// and `CodeBlock` reads it back as a raw string.

// A word plus its trailing whitespace, OR a run of standalone whitespace (e.g.
// leading indentation); together these tile the text so every character is kept.
const WORD_OR_WHITESPACE = /\S+\s*|\s+/g;

function textNode(value: string): Text {
  return { type: 'text', value };
}

function wordFadeSpan(value: string): Element {
  return {
    type: 'element',
    tagName: 'span',
    properties: { className: ['word-fade'] },
    children: [textNode(value)],
  };
}

// Split one text node into a span per word (trailing whitespace kept on the
// word); pure-whitespace chunks pass through as plain text so spacing survives.
function splitTextNode(node: Text): Array<Element | Text> {
  const chunks = node.value.match(WORD_OR_WHITESPACE);
  if (!chunks) return [node];
  return chunks.map((chunk) => (/\S/.test(chunk) ? wordFadeSpan(chunk) : textNode(chunk)));
}

// Recurse into an element, wrapping descendant words -- but never the contents of
// <pre>/<code>, which are left verbatim for the syntax-highlighted code path.
function wrapElementWords(element: Element): void {
  if (element.tagName === 'pre' || element.tagName === 'code') return;
  const next: ElementContent[] = [];
  for (const child of element.children) {
    if (child.type === 'text') {
      next.push(...splitTextNode(child));
    } else {
      if (child.type === 'element') wrapElementWords(child);
      next.push(child);
    }
  }
  element.children = next;
}

// rehype plugin: walk the hast tree and wrap every (non-code) word in a
// `.word-fade` span. Mutates the tree in place per the unified plugin contract.
function rehypeWordFade() {
  return (tree: Root): undefined => {
    const next: RootContent[] = [];
    for (const child of tree.children) {
      if (child.type === 'text') {
        next.push(...splitTextNode(child));
      } else {
        if (child.type === 'element') wrapElementWords(child);
        next.push(child);
      }
    }
    tree.children = next;
    return undefined;
  };
}

// Stable rehype-plugin list for the animated path (avoids re-allocating on every
// render); the static path passes no rehype plugins so text renders plainly.
const WORD_FADE_REHYPE_PLUGINS = [rehypeWordFade];

type CodeBlockProps = {
  language: string;
  value: string;
};

function CodeBlock({ language, value }: CodeBlockProps): JSX.Element {
  return (
    <Highlight theme={CODE_THEME} code={value} language={language}>
      {({ tokens, getLineProps, getTokenProps }) => (
        <pre className="md-code" data-language={language}>
          <code className="md-code__content">
            {tokens.map((line, lineIndex) => {
              const lineProps = getLineProps({ line });
              return (
                <span key={lineIndex} className="md-code__line" style={lineProps.style}>
                  {line.map((token, tokenIndex) => {
                    const tokenProps = getTokenProps({ token });
                    return (
                      <span key={tokenIndex} className={tokenProps.className} style={tokenProps.style}>
                        {tokenProps.children}
                      </span>
                    );
                  })}
                  {/* Re-emit the newline prism strips so blank lines survive and
                      `white-space: pre` lays the block out correctly. */}
                  {lineIndex < tokens.length - 1 ? '\n' : null}
                </span>
              );
            })}
          </code>
        </pre>
      )}
    </Highlight>
  );
}

const MARKDOWN_COMPONENTS: Components = {
  // react-markdown wraps fenced code in `<pre><code>`. `CodeBlock` renders its
  // own `<pre class="md-code">`, so collapse the default `<pre>` to a passthrough
  // to avoid nesting two `<pre>` elements.
  pre({ children }) {
    return <>{children}</>;
  },
  code({ className, children }) {
    const match = LANGUAGE_CLASS.exec(className ?? '');
    const raw = String(children ?? '');
    // Treat anything with a language class -- or any multi-line content -- as a
    // block; everything else is inline code.
    const isBlock = match !== null || raw.includes('\n');
    if (isBlock) {
      return <CodeBlock language={match?.[1] ?? 'text'} value={raw.replace(/\n$/, '')} />;
    }
    return <code className="md-inline-code">{children}</code>;
  },
  // Wrap wide tables so they scroll horizontally instead of overflowing the chat.
  table({ children }) {
    return (
      <div className="md-table-wrap">
        <table className="md-table">{children}</table>
      </div>
    );
  },
  // Links always open externally and never leak the opener reference.
  a({ href, title, children }) {
    return (
      <a href={href} title={title} target="_blank" rel="noreferrer noopener">
        {children}
      </a>
    );
  },
};

function MarkdownView({ text, animate = false }: MarkdownProps): JSX.Element {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={animate ? WORD_FADE_REHYPE_PLUGINS : undefined}
        components={MARKDOWN_COMPONENTS}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

export const Markdown = memo(MarkdownView);
