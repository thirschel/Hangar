import { memo } from 'react';
import type { JSX } from 'react';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Highlight, themes } from 'prism-react-renderer';

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

function MarkdownView({ text }: MarkdownProps): JSX.Element {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={REMARK_PLUGINS} components={MARKDOWN_COMPONENTS}>
        {text}
      </ReactMarkdown>
    </div>
  );
}

export const Markdown = memo(MarkdownView);
