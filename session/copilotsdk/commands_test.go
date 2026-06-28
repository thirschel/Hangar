package copilotsdk

import (
	"testing"

	csrpc "github.com/github/copilot-sdk/go/rpc"
)

func TestNormalizeCommandResultText(t *testing.T) {
	md := true
	got := normalizeCommandResult(&csrpc.SlashCommandTextResult{Text: "usage", Markdown: &md})
	if got.Kind != CommandResultText || got.Text != "usage" || !got.Markdown {
		t.Fatalf("text result = %+v", got)
	}
}

func TestNormalizeCommandResultAgentPrompt(t *testing.T) {
	got := normalizeCommandResult(&csrpc.SlashCommandAgentPromptResult{Prompt: "Explain X", DisplayPrompt: "/explain"})
	if got.Kind != CommandResultAgentPrompt || got.Prompt != "Explain X" || got.DisplayPrompt != "/explain" {
		t.Fatalf("agentPrompt result = %+v", got)
	}
}

func TestNormalizeCommandResultCompleted(t *testing.T) {
	msg := "cleared"
	got := normalizeCommandResult(&csrpc.SlashCommandCompletedResult{Message: &msg})
	if got.Kind != CommandResultCompleted || got.Message != "cleared" {
		t.Fatalf("completed result = %+v", got)
	}
}

func TestNormalizeCommandResultSubcommand(t *testing.T) {
	grp := "OpenAI"
	got := normalizeCommandResult(&csrpc.SlashCommandSelectSubcommandResult{
		Title:   "Pick a model",
		Command: "model",
		Options: []csrpc.SlashCommandSelectSubcommandOption{{Name: "gpt-5", Description: "GPT-5", Group: &grp}},
	})
	if got.Kind != CommandResultSubcommand || got.SubcommandCommand != "model" || got.SubcommandTitle != "Pick a model" ||
		len(got.SubcommandOptions) != 1 || got.SubcommandOptions[0].Name != "gpt-5" || got.SubcommandOptions[0].Group != "OpenAI" {
		t.Fatalf("subcommand result = %+v", got)
	}
}

func TestNormalizeCommandResultUnknownIsCompleted(t *testing.T) {
	if got := normalizeCommandResult(nil); got.Kind != CommandResultCompleted {
		t.Fatalf("nil result = %+v, want completed no-op", got)
	}
}
