package git

import (
	"testing"
)

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple lowercase string",
			input:    "feature",
			expected: "feature",
		},
		{
			name:     "string with spaces",
			input:    "new feature branch",
			expected: "new-feature-branch",
		},
		{
			name:     "mixed case string",
			input:    "FeAtUrE BrAnCh",
			expected: "feature-branch",
		},
		{
			name:     "string with special characters",
			input:    "feature!@#$%^&*()",
			expected: "feature",
		},
		{
			name:     "slashes and dots are stripped (F-14)",
			input:    "feature/sub_branch.v1",
			expected: "featuresub_branchv1",
		},
		{
			name:     "string with multiple dashes",
			input:    "feature---branch",
			expected: "feature-branch",
		},
		{
			name:     "string with leading and trailing dashes",
			input:    "-feature-branch-",
			expected: "feature-branch",
		},
		{
			name:     "slashes stripped entirely (F-14)",
			input:    "/feature/branch/",
			expected: "featurebranch",
		},
		{
			name:     "empty string falls back to session",
			input:    "",
			expected: "session",
		},
		{
			name:     "traversal sequence cannot escape (F-14)",
			input:    "../../etc",
			expected: "etc",
		},
		{
			name:     "windows traversal sequence cannot escape (F-14)",
			input:    "..\\..\\Windows",
			expected: "windows",
		},
		{
			name:     "all-traversal title falls back to session (F-14)",
			input:    "../../",
			expected: "session",
		},
		{
			name:     "complex mixed case with special chars",
			input:    "USER/Feature Branch!@#$%^&*()/v1.0",
			expected: "userfeature-branchv10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranchName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
