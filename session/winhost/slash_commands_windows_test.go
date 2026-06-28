//go:build windows

package winhost

import (
	"testing"

	"hangar/session/copilotsdk"
	"hangar/session/winhost/proto"
)

// TestCommandInfosMapping proves copilotsdk.CommandDetail -> proto.CommandInfo keeps
// the fields the composer autocomplete renders (name/description/kind/aliases/hint).
func TestCommandInfosMapping(t *testing.T) {
	got := commandInfos([]copilotsdk.CommandDetail{
		{Name: "help", Description: "Show help", Kind: "builtin", Aliases: []string{"h"}},
		{Name: "explain", Description: "Explain", Kind: "skill", InputHint: "file"},
	})
	if len(got) != 2 || got[0].Name != "help" || got[0].Kind != "builtin" || len(got[0].Aliases) != 1 ||
		got[1].Kind != "skill" || got[1].InputHint != "file" {
		t.Fatalf("commandInfos = %+v", got)
	}
}

// TestCommandResultMapping proves the normalized copilotsdk.CommandResult -> proto
// mapping preserves each variant's fields (here the subcommand picker).
func TestCommandResultMapping(t *testing.T) {
	got := commandResult(copilotsdk.CommandResult{
		Kind:              copilotsdk.CommandResultSubcommand,
		SubcommandTitle:   "Pick",
		SubcommandCommand: "model",
		SubcommandOptions: []copilotsdk.SubcommandOption{{Name: "gpt-5", Description: "GPT-5", Group: "OpenAI"}},
	})
	if got == nil || got.Kind != proto.CommandResultSubcommand || got.SubcommandCommand != "model" ||
		len(got.SubcommandOptions) != 1 || got.SubcommandOptions[0].Name != "gpt-5" || got.SubcommandOptions[0].Group != "OpenAI" {
		t.Fatalf("commandResult = %+v", got)
	}

	text := commandResult(copilotsdk.CommandResult{Kind: copilotsdk.CommandResultText, Text: "usage", Markdown: true})
	if text.Kind != proto.CommandResultText || text.Text != "usage" || !text.Markdown {
		t.Fatalf("text commandResult = %+v", text)
	}
}
