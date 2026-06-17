package promptpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"

	cslog "claude-squad/log"
)

type Category string

const (
	CategoryUnknown              Category = "unknown"
	CategoryGenericContinue      Category = "generic-continue"
	CategoryShellExec            Category = "shell-exec"
	CategoryFileWrite            Category = "file-write"
	CategoryMCPTrust             Category = "mcp-trust"
	CategoryTrustFolder          Category = "trust-folder"
	CategoryPersistentPermission Category = "persistent-permission"
)

type Match struct {
	Program              string
	Category             Category
	RuleID               string
	ApproveKeys          []byte
	MatchedText          string
	Fingerprint          string
	HasPersistenceOption bool
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\a]*(?:\a|\x1b\\)`)

func Classify(program, screen string) (Match, bool) {
	tail := normalizeTail(screen, 24)
	if tail == "" {
		return Match{}, false
	}
	hasPersistence := hasPersistenceOption(tail)
	mk := func(cat Category, rule string, keys []byte) (Match, bool) {
		if len(keys) == 0 {
			keys = []byte{'\r'}
		}
		sum := sha256.Sum256([]byte(string(cat) + "\n" + tail))
		return Match{
			Program:              program,
			Category:             cat,
			RuleID:               rule,
			ApproveKeys:          keys,
			MatchedText:          truncate(tail, 240),
			Fingerprint:          hex.EncodeToString(sum[:]),
			HasPersistenceOption: hasPersistence,
		}, true
	}

	switch {
	case containsAny(tail,
		"new mcp server", "mcp server", "model context protocol",
		"trust this tool", "allow this tool", "tool trust", "untrusted tool", "install this tool"):
		return mk(CategoryMCPTrust, "mcp-trust", nil)
	case containsAny(tail, "do you trust the files in this folder", "trust this folder", "remember this folder"):
		return mk(CategoryTrustFolder, "trust-folder", nil)
	case containsAny(tail,
		"do you want to run this command", "run this command", "execute this command",
		"execute command", "shell command", "allow command", "approve command",
		"command to run", "run `", "run the following command"):
		return mk(CategoryShellExec, "shell-exec", nil)
	case containsAny(tail,
		"write file", "edit file", "create file", "delete file", "overwrite file",
		"modify file", "apply patch", "make the following changes", "apply these changes",
		"save changes", "outside the workspace", "outside workspace"):
		return mk(CategoryFileWrite, "file-write", nil)
	case grantsStandingPermission(tail):
		return mk(CategoryPersistentPermission, "persistent-permission", nil)
	case isAiderDocsPrompt(program, tail), isGeminiDocsPrompt(program, tail):
		return mk(CategoryGenericContinue, "agent-docs-confirm", []byte{'D', '\r'})
	case isBenignContinue(program, tail):
		return mk(CategoryGenericContinue, "benign-continue", nil)
	}
	return Match{}, false
}

func AllowsAutoApprove(m Match) bool {
	return m.Category == CategoryGenericContinue
}

func IsPrompt(program, screen string) bool {
	_, ok := Classify(program, screen)
	return ok
}

func AuditAutoApproval(session, program string, m Match, source string) {
	if cslog.InfoLog == nil {
		return
	}
	cslog.InfoLog.Printf("AutoYes approved session=%q program=%q category=%s rule=%s source=%s matched=%q",
		session, program, m.Category, m.RuleID, source, m.MatchedText)
}

func normalizeTail(s string, maxLines int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = ansiRE.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(strings.ToLower(line)), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isBenignContinue(_ string, tail string) bool {
	if containsAny(tail,
		"press enter to continue", "enter to continue", "continue?", "continue (y/n)",
		"would you like to continue", "do you want to continue", "proceed?", "proceed (y/n)",
		"proceed with", "is this ok", "is that ok", "confirm?") {
		return true
	}
	return false
}

func isAiderDocsPrompt(program, tail string) bool {
	return strings.Contains(program, "aider") && strings.Contains(tail, "open documentation url for more info")
}

func isGeminiDocsPrompt(program, tail string) bool {
	return strings.Contains(program, "gemini") && strings.Contains(tail, "open documentation url for more info")
}

func hasPersistenceOption(tail string) bool {
	return containsAny(tail, "don't ask again", "dont ask again", "do not ask again", "remember this", "always allow", "always trust")
}

func grantsStandingPermission(tail string) bool {
	if containsAny(tail, "always allow", "always trust", "remember this folder", "grant persistent", "standing permission") {
		return true
	}
	if containsAny(tail, "don't ask again for", "dont ask again for", "do not ask again for", "yes, and don't ask again") {
		return true
	}
	return false
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
