package copilotsdk

import (
	"context"
	"fmt"

	copilot "github.com/github/copilot-sdk/go"
	csrpc "github.com/github/copilot-sdk/go/rpc"
)

// CommandDetail is a display-safe slash command for the rich composer's autocomplete.
// Kind is "builtin" | "skill" | "client". winhost maps it to proto.CommandInfo.
type CommandDetail struct {
	Name        string
	Description string
	Kind        string
	Aliases     []string
	InputHint   string
}

// SubcommandOption is one selectable subcommand returned by an InvokeCommand that
// needs disambiguation (e.g. /model -> pick a model).
type SubcommandOption struct {
	Name        string
	Description string
	Group       string
}

// Command result kinds for CommandResult.Kind.
const (
	CommandResultText        = "text"
	CommandResultAgentPrompt = "agentPrompt"
	CommandResultCompleted   = "completed"
	CommandResultSubcommand  = "subcommand"
)

// CommandResult is the normalized result of invoking a slash command. Only the
// fields relevant to Kind are populated.
type CommandResult struct {
	Kind              string
	Text              string
	Markdown          bool
	Prompt            string
	DisplayPrompt     string
	Message           string
	SubcommandTitle   string
	SubcommandCommand string
	SubcommandOptions []SubcommandOption
}

// ListCommands returns the session's available slash commands (runtime builtins,
// skill-backed commands, and SDK/client commands) for the composer autocomplete.
func (s *Session) ListCommands(ctx context.Context) ([]CommandDetail, error) {
	sess := s.session()
	if sess == nil {
		return nil, fmt.Errorf("session not started")
	}
	if sess.RPC == nil || sess.RPC.Commands == nil {
		return nil, nil
	}
	res, err := sess.RPC.Commands.List(ctx, &csrpc.CommandsListRequest{
		IncludeBuiltins:       copilot.Bool(true),
		IncludeClientCommands: copilot.Bool(true),
		IncludeSkills:         copilot.Bool(true),
	})
	if err != nil {
		return nil, s.noteErr(err)
	}
	if res == nil {
		return nil, nil
	}
	out := make([]CommandDetail, 0, len(res.Commands))
	for _, c := range res.Commands {
		hint := ""
		if c.Input != nil {
			hint = c.Input.Hint
		}
		out = append(out, CommandDetail{
			Name:        c.Name,
			Description: c.Description,
			Kind:        string(c.Kind),
			Aliases:     append([]string(nil), c.Aliases...),
			InputHint:   hint,
		})
	}
	return out, nil
}

// InvokeCommand runs a slash command (name without a leading slash; input is the raw
// args after the name) and returns the normalized result for the client to act on.
func (s *Session) InvokeCommand(ctx context.Context, name, input string) (CommandResult, error) {
	sess := s.session()
	if sess == nil {
		return CommandResult{}, fmt.Errorf("session not started")
	}
	if sess.RPC == nil || sess.RPC.Commands == nil {
		return CommandResult{}, fmt.Errorf("session commands RPC is unavailable")
	}
	req := &csrpc.CommandsInvokeRequest{Name: name}
	if input != "" {
		req.Input = &input
	}
	res, err := sess.RPC.Commands.Invoke(ctx, req)
	if err != nil {
		return CommandResult{}, s.noteErr(err)
	}
	return normalizeCommandResult(res), nil
}

// normalizeCommandResult flattens the SDK's slash-command result union into a single
// CommandResult the wire protocol and desktop can consume. An unknown/nil result is
// treated as a no-op "completed".
func normalizeCommandResult(res csrpc.SlashCommandInvocationResult) CommandResult {
	switch r := res.(type) {
	case *csrpc.SlashCommandTextResult:
		return CommandResult{Kind: CommandResultText, Text: r.Text, Markdown: boolVal(r.Markdown)}
	case *csrpc.SlashCommandAgentPromptResult:
		return CommandResult{Kind: CommandResultAgentPrompt, Prompt: r.Prompt, DisplayPrompt: r.DisplayPrompt}
	case *csrpc.SlashCommandCompletedResult:
		msg := ""
		if r.Message != nil {
			msg = *r.Message
		}
		return CommandResult{Kind: CommandResultCompleted, Message: msg}
	case *csrpc.SlashCommandSelectSubcommandResult:
		opts := make([]SubcommandOption, 0, len(r.Options))
		for _, o := range r.Options {
			group := ""
			if o.Group != nil {
				group = *o.Group
			}
			opts = append(opts, SubcommandOption{Name: o.Name, Description: o.Description, Group: group})
		}
		return CommandResult{Kind: CommandResultSubcommand, SubcommandTitle: r.Title, SubcommandCommand: r.Command, SubcommandOptions: opts}
	default:
		return CommandResult{Kind: CommandResultCompleted}
	}
}

func boolVal(p *bool) bool { return p != nil && *p }
