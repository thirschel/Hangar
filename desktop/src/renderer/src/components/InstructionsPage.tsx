import type { JSX } from 'react';
import type { InstructionInfo } from '../../../main/host-client';

// Instructions page (full-middle, read-only). Renders the latest `instructions`
// snapshot the SDK loaded for the session: one card per source with its label, a
// location/type badge, description, applies-to globs, on-disk path, and the raw
// file content. Purely informational -- there are no toggles or actions here.
export function InstructionsPage({
  instructions,
}: {
  instructions: InstructionInfo[];
}): JSX.Element {
  if (instructions.length === 0) {
    return (
      <div className="instructions-page instructions-page--empty">
        <p className="instructions-page__empty-text">No custom instructions loaded.</p>
      </div>
    );
  }

  return (
    <div className="instructions-page" aria-label="Instructions">
      {instructions.map((instr, i) => {
        const badge = instr.location || instr.type;
        return (
          <article
            key={`${instr.sourcePath ?? instr.label}:${i}`}
            className="instructions-page__card"
          >
            <header className="instructions-page__head">
              <span className="instructions-page__label">{instr.label}</span>
              {badge && <span className="instructions-page__badge">{badge}</span>}
            </header>

            {instr.description && (
              <p className="instructions-page__description">{instr.description}</p>
            )}

            <dl className="instructions-page__meta">
              {instr.sourcePath && (
                <div className="instructions-page__meta-row">
                  <dt className="instructions-page__meta-key">Path</dt>
                  <dd className="instructions-page__meta-val instructions-page__path">
                    {instr.sourcePath}
                  </dd>
                </div>
              )}
              {instr.applyTo && instr.applyTo.length > 0 && (
                <div className="instructions-page__meta-row">
                  <dt className="instructions-page__meta-key">Applies to</dt>
                  <dd className="instructions-page__meta-val">{instr.applyTo.join(', ')}</dd>
                </div>
              )}
            </dl>

            {instr.content && <pre className="instructions-page__content">{instr.content}</pre>}
          </article>
        );
      })}
    </div>
  );
}
