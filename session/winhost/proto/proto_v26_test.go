package proto

import (
	"encoding/json"
	"testing"
)

func TestVersionV26(t *testing.T) {
	// v26 (slash commands) is a floor, not the current version: once shipped, its
	// surface must remain present in every later version.
	if Version < 26 {
		t.Fatalf("Version = %d, want >= 26", Version)
	}
}

// TestV26MethodNames pins the wire names the desktop and host agree on for the
// slash-command list/invoke RPCs.
func TestV26MethodNames(t *testing.T) {
	cases := map[string]string{
		MethodListCommands:  "ListCommands",
		MethodInvokeCommand: "InvokeCommand",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("method = %q, want %q", got, want)
		}
	}
}

// TestListCommandsResponseRoundTrip guards the MethodListCommands result shape: a
// list of CommandInfo with name/description/kind/aliases for the composer autocomplete.
func TestListCommandsResponseRoundTrip(t *testing.T) {
	resp := Response{ID: 1, OK: true, Commands: []CommandInfo{
		{Name: "help", Description: "Show help", Kind: "builtin"},
		{Name: "explain", Description: "Explain code", Kind: "skill", Aliases: []string{"exp"}, InputHint: "file"},
	}}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Response
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Commands) != 2 || got.Commands[0].Name != "help" || got.Commands[0].Kind != "builtin" ||
		got.Commands[1].Kind != "skill" || len(got.Commands[1].Aliases) != 1 || got.Commands[1].InputHint != "file" {
		t.Fatalf("commands round-trip = %+v", got.Commands)
	}
	if !containsKey(b, "commands") {
		t.Fatalf("expected %q key, got %s", "commands", b)
	}
}

// TestInvokeCommandRequestRoundTrip guards the MethodInvokeCommand request shape:
// the command name plus the raw input args.
func TestInvokeCommandRequestRoundTrip(t *testing.T) {
	req := Request{ID: 2, Method: MethodInvokeCommand, Session: "ws", Command: "model", Input: "gpt-5"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Method != MethodInvokeCommand || got.Command != "model" || got.Input != "gpt-5" {
		t.Fatalf("invoke request round-trip = %+v", got)
	}
	for _, key := range []string{"command", "input"} {
		if !containsKey(b, key) {
			t.Fatalf("expected %q key, got %s", key, b)
		}
	}
}

// TestCommandResultRoundTrip guards each CommandResult variant kind serializes and
// deserializes with its relevant fields.
func TestCommandResultRoundTrip(t *testing.T) {
	cases := []*CommandResult{
		{Kind: CommandResultText, Text: "usage: ...", Markdown: true},
		{Kind: CommandResultAgentPrompt, Prompt: "Explain X", DisplayPrompt: "/explain"},
		{Kind: CommandResultCompleted, Message: "cleared"},
		{Kind: CommandResultSubcommand, SubcommandTitle: "Pick a model", SubcommandCommand: "model",
			SubcommandOptions: []SubcommandOption{{Name: "gpt-5", Description: "GPT-5", Group: "OpenAI"}}},
	}
	for _, want := range cases {
		resp := Response{ID: 3, OK: true, CommandResult: want}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("Marshal %s: %v", want.Kind, err)
		}
		var got Response
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal %s: %v", want.Kind, err)
		}
		if got.CommandResult == nil || got.CommandResult.Kind != want.Kind {
			t.Fatalf("result kind = %+v, want %s", got.CommandResult, want.Kind)
		}
		switch want.Kind {
		case CommandResultText:
			if got.CommandResult.Text != "usage: ..." || !got.CommandResult.Markdown {
				t.Fatalf("text result = %+v", got.CommandResult)
			}
		case CommandResultAgentPrompt:
			if got.CommandResult.Prompt != "Explain X" || got.CommandResult.DisplayPrompt != "/explain" {
				t.Fatalf("agentPrompt result = %+v", got.CommandResult)
			}
		case CommandResultCompleted:
			if got.CommandResult.Message != "cleared" {
				t.Fatalf("completed result = %+v", got.CommandResult)
			}
		case CommandResultSubcommand:
			if len(got.CommandResult.SubcommandOptions) != 1 || got.CommandResult.SubcommandOptions[0].Name != "gpt-5" ||
				got.CommandResult.SubcommandCommand != "model" {
				t.Fatalf("subcommand result = %+v", got.CommandResult)
			}
		}
	}
}
