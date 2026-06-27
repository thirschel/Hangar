// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { InstructionInfo } from '../../../../main/host-client';
import { InstructionsPage } from '../InstructionsPage';

describe('InstructionsPage', () => {
  it('shows an empty state when nothing is loaded', () => {
    render(<InstructionsPage instructions={[]} />);
    expect(screen.getByText('No custom instructions loaded.')).toBeInTheDocument();
  });

  it('renders a card per source with its label, badge, path, globs and content', () => {
    const instructions: InstructionInfo[] = [
      {
        label: 'Repo instructions',
        sourcePath: '.github/copilot-instructions.md',
        type: 'repository',
        location: 'repository',
        description: 'House style',
        applyTo: ['**/*.go', '**/*.ts'],
        content: 'Always run gofmt.',
      },
      { label: 'Home instructions' },
    ];

    render(<InstructionsPage instructions={instructions} />);

    expect(screen.getByText('Repo instructions')).toBeInTheDocument();
    expect(screen.getByText('repository')).toBeInTheDocument(); // location/type badge
    expect(screen.getByText('House style')).toBeInTheDocument();
    expect(screen.getByText('.github/copilot-instructions.md')).toBeInTheDocument();
    expect(screen.getByText('**/*.go, **/*.ts')).toBeInTheDocument();
    expect(screen.getByText('Always run gofmt.')).toBeInTheDocument();
    // A bare source still renders its label without crashing on missing fields.
    expect(screen.getByText('Home instructions')).toBeInTheDocument();
  });

  it('falls back to the type when no location is present for the badge', () => {
    render(<InstructionsPage instructions={[{ label: 'X', type: 'agents-md' }]} />);
    expect(screen.getByText('agents-md')).toBeInTheDocument();
  });
});
