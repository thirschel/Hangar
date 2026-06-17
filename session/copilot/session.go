package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const displaySnippetRunes = 60

type Session struct {
	ID         string
	Dir        string
	Name       string
	Repository string
	OriginRoot string
	OriginHead string
	OriginRef  string
	Cwd        string
	Branch     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	HasEvents  bool
	InUse      bool

	firstUserMsg string
}

// DisplayName returns a user-facing name for the session.
func (s Session) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	if snippet := messageSnippet(s.firstUserMsg); snippet != "" {
		return snippet
	}
	if s.Repository != "" && s.Branch != "" {
		return s.Repository + "@" + s.Branch
	}
	return shortID(s.ID)
}

// FirstUserMessage returns the first user.message content from the session.
func FirstUserMessage(s Session) (string, error) {
	if s.firstUserMsg != "" {
		return s.firstUserMsg, nil
	}

	eventsPath := filepath.Join(s.Dir, "events.jsonl")
	if info, err := os.Stat(eventsPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	} else if info.IsDir() || info.Size() == 0 {
		return "", nil
	}

	result, err := scanEvents(eventsPath, false, true)
	if err != nil {
		return "", err
	}
	return result.firstUserMsg, nil
}

func messageSnippet(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}

	msg = strings.Join(strings.Fields(msg), " ")
	runes := []rune(msg)
	if len(runes) > displaySnippetRunes {
		return string(runes[:displaySnippetRunes])
	}
	return msg
}

func shortID(id string) string {
	runes := []rune(id)
	if len(runes) > 8 {
		return string(runes[:8])
	}
	return id
}
