import type { JSX } from 'react';
import type { SkillInfo } from '../../../main/host-client';

// Skills page (full-middle, read-only). Renders the latest `skills` snapshot
// accumulated by ChatViewHost: one card per skill with its name, an enabled
// indicator, description, source and on-disk path. Purely informational -- there
// are no toggles or actions here.
export function SkillsPage({ skills }: { skills: SkillInfo[] }): JSX.Element {
  if (skills.length === 0) {
    return (
      <div className="skills-page skills-page--empty">
        <p className="skills-page__empty-text">No skills available.</p>
      </div>
    );
  }

  return (
    <div className="skills-page" aria-label="Skills">
      {skills.map((skill) => (
        <article key={skill.name} className="skills-page__card">
          <header className="skills-page__head">
            <span className="skills-page__name">{skill.name}</span>
            <span
              className={`skills-page__enabled skills-page__enabled--${skill.enabled ? 'on' : 'off'}`}
            >
              {skill.enabled ? 'Enabled' : 'Disabled'}
            </span>
          </header>

          {skill.description && <p className="skills-page__description">{skill.description}</p>}

          <dl className="skills-page__meta">
            {skill.source && (
              <div className="skills-page__meta-row">
                <dt className="skills-page__meta-key">Source</dt>
                <dd className="skills-page__meta-val">{skill.source}</dd>
              </div>
            )}
            {skill.path && (
              <div className="skills-page__meta-row">
                <dt className="skills-page__meta-key">Path</dt>
                <dd className="skills-page__meta-val skills-page__path">{skill.path}</dd>
              </div>
            )}
          </dl>
        </article>
      ))}
    </div>
  );
}
