package promptpolicy

import "testing"

func TestClassifyBlocksDangerousPrompts(t *testing.T) {
	tests := []struct {
		name string
		text string
		want Category
	}{
		{
			name: "shell exec",
			text: "Do you want to run this command?\n  1. Yes\n  3. No, and tell Copilot what to do differently",
			want: CategoryShellExec,
		},
		{
			name: "file write",
			text: "Claude wants to edit file app.go and make the following changes.\nNo, and tell Claude what to do differently",
			want: CategoryFileWrite,
		},
		{
			name: "mcp trust",
			text: "A new MCP server was detected. Trust this server?\nNo, and tell Claude what to do differently",
			want: CategoryMCPTrust,
		},
		{
			name: "standing permission",
			text: "Grant persistent permission? Yes, and don't ask again for this decision",
			want: CategoryPersistentPermission,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Classify("copilot", tt.text)
			if !ok {
				t.Fatal("expected prompt match")
			}
			if got.Category != tt.want {
				t.Fatalf("Category = %s, want %s", got.Category, tt.want)
			}
			if AllowsAutoApprove(got) {
				t.Fatalf("%s must not auto-approve", got.Category)
			}
		})
	}
}

func TestClassifyAllowsBenignContinuePrompts(t *testing.T) {
	tests := []struct {
		name    string
		program string
		text    string
	}{
		{"plain continue", "copilot", "Press enter to continue"},
		{"aider confirm with dont ask option", "aider", "Proceed with this benign answer?\n(Y)es/(N)o/(D)on't ask again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Classify(tt.program, tt.text)
			if !ok {
				t.Fatal("expected prompt match")
			}
			if got.Category != CategoryGenericContinue {
				t.Fatalf("Category = %s, want %s", got.Category, CategoryGenericContinue)
			}
			if !AllowsAutoApprove(got) {
				t.Fatalf("expected %s to auto-approve", got.Category)
			}
		})
	}
}
