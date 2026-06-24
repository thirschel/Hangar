// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CreateWorkspaceModal } from '../components/CreateWorkspaceModal';

describe('CreateWorkspaceModal', () => {
  it('offers the rich toggle for a Copilot agent and passes rich=true on create', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(<CreateWorkspaceModal onClose={() => {}} onCreate={onCreate} initialRepoPath="C:/repo" />);

    // window.cs.getDefaultProgram() resolves to 'copilot' (setup mock) => toggle shows.
    const richCheckbox = await screen.findByLabelText(/Rich agent view/i);
    await act(async () => {
      fireEvent.click(richCheckbox);
    });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /Create workspace/i }));
    });

    await waitFor(() => expect(onCreate).toHaveBeenCalledTimes(1));
    expect(onCreate.mock.calls[0][0]).toMatchObject({ repoPath: 'C:/repo', rich: true });
  });

  it('hides the rich toggle for a non-Copilot agent', async () => {
    render(<CreateWorkspaceModal onClose={() => {}} onCreate={vi.fn()} initialRepoPath="C:/repo" />);
    await screen.findByLabelText(/Rich agent view/i); // wait until the copilot default loads
    const agentInput = screen.getByPlaceholderText('copilot');
    await act(async () => {
      fireEvent.change(agentInput, { target: { value: 'aider' } });
    });
    expect(screen.queryByLabelText(/Rich agent view/i)).not.toBeInTheDocument();
  });
});
