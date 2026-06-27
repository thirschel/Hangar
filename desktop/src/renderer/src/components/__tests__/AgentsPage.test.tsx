// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { AgentInfo } from '../../../../main/host-client';
import { AgentsPage } from '../AgentsPage';

describe('AgentsPage', () => {
  it('shows an empty state when no agents are discovered', () => {
    render(<AgentsPage agents={[]} />);
    expect(screen.getByText('No custom agents discovered.')).toBeInTheDocument();
  });

  it('renders a card per agent with name, source, model, tools, mcp and path', () => {
    const agents: AgentInfo[] = [
      {
        name: 'reviewer',
        displayName: 'Code Reviewer',
        description: 'Reviews diffs',
        model: 'gpt-5',
        path: '/home/u/.copilot/agents/reviewer.md',
        source: 'user',
        skills: ['pdf'],
        tools: ['read', 'write'],
        mcpServerNames: ['github'],
        userInvocable: true,
      },
      { name: 'helper', userInvocable: false },
    ];

    render(<AgentsPage agents={agents} />);

    expect(screen.getByText('Code Reviewer')).toBeInTheDocument();
    expect(screen.getByText('user')).toBeInTheDocument(); // source badge
    expect(screen.getByText('Reviews diffs')).toBeInTheDocument();
    expect(screen.getByText('gpt-5')).toBeInTheDocument();
    expect(screen.getByText('read, write')).toBeInTheDocument();
    expect(screen.getByText('github')).toBeInTheDocument();
    expect(screen.getByText('/home/u/.copilot/agents/reviewer.md')).toBeInTheDocument();
    // A name-only agent renders its name and, when not invocable, a Subagent badge.
    expect(screen.getByText('helper')).toBeInTheDocument();
    expect(screen.getByText('Subagent')).toBeInTheDocument();
  });
});
