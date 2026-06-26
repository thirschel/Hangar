// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CreateWorkspaceModal } from '../components/CreateWorkspaceModal';

describe('CreateWorkspaceModal', () => {
  it('creates a terminal workspace by default and never passes rich', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(<CreateWorkspaceModal onClose={() => {}} onCreate={onCreate} initialRepoPath="C:/repo" />);

    // Wait for the async default program ('copilot' from the setup mock) to load
    // into the agent field, flushing the effect's state update.
    await screen.findByDisplayValue('copilot');

    // The "Rich agent view" toggle was removed -- standard creation is always a
    // terminal worktree; rich chats are created only from the agent-mode path.
    expect(screen.queryByLabelText(/Rich agent view/i)).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Create workspace/i }));
    });

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate.mock.calls[0][0]).toMatchObject({ repoPath: 'C:/repo' });
    expect(onCreate.mock.calls[0][0].rich).toBeUndefined();
  });

  it('offers no rich toggle even after changing the agent', async () => {
    render(<CreateWorkspaceModal onClose={() => {}} onCreate={vi.fn()} initialRepoPath="C:/repo" />);
    await screen.findByDisplayValue('copilot'); // wait until the copilot default loads

    const agentInput = screen.getByPlaceholderText('copilot');
    await act(async () => {
      fireEvent.change(agentInput, { target: { value: 'aider' } });
    });

    expect(screen.queryByLabelText(/Rich agent view/i)).not.toBeInTheDocument();
  });

  it('hides the Agent + Shell fields and forces a rich Copilot chat when rich', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(
      <CreateWorkspaceModal
        onClose={() => {}}
        onCreate={onCreate}
        initialRepoPath="C:/repo"
        rich
      />,
    );

    // Rich is Copilot-only and SDK-backed, so the terminal-shaped fields are gone.
    expect(screen.queryByPlaceholderText('copilot')).not.toBeInTheDocument();
    expect(screen.queryByText('Shell')).not.toBeInTheDocument();
    // The screen decides rich, so there is still no in-modal toggle.
    expect(screen.queryByLabelText(/Rich agent view/i)).not.toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Create chat/i }));
    });

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      repoPath: 'C:/repo',
      program: 'copilot',
      rich: true,
    });
    // No terminal shell is sent for a rich chat.
    expect(onCreate.mock.calls[0][0].shell).toBeUndefined();
  });
});