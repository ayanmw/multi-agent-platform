package tool

import (
	"testing"
)

func TestParseShellCommand(t *testing.T) {
	tests := []struct {
		name         string
		cmd          string
		wantProgram  string
		wantArgs     []string
		wantErr      bool
	}{
		{"simple", "echo hello", "echo", []string{"hello"}, false},
		{"with placeholders", "echo {message}", "echo", []string{"{message}"}, false},
		{"multiple args", "ls -la /tmp", "ls", []string{"-la", "/tmp"}, false},
		{"double quotes", `echo "hello world"`, "echo", []string{"hello world"}, false},
		{"single quotes", `echo 'hello world'`, "echo", []string{"hello world"}, false},
		{"mixed quotes", `echo "hello" 'world'`, "echo", []string{"hello", "world"}, false},
		{"escaped space", `echo hello\ world`, "echo", []string{"hello world"}, false},
		{"placeholder in quotes", `echo "{message}"`, "echo", []string{"{message}"}, false},
		{"empty", "", "", nil, true},
		{"only whitespace", "   ", "", nil, true},
		{"unmatched double", `"hello`, "", nil, true},
		{"unmatched single", `'hello`, "", nil, true},
		{"trailing backslash keeps literal", `echo \\`, "echo", []string{"\\"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, args, err := ParseShellCommand(tt.cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got program=%q args=%v", prog, args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prog != tt.wantProgram {
				t.Errorf("program = %q, want %q", prog, tt.wantProgram)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("arg[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestHasShellMetacharacters(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"echo hello", false},
		{"echo {name}", false},
		{"cat a | grep b", true},
		{"cmd1 && cmd2", true},
		{"cmd1; cmd2", true},
		{"echo $(id)", true},
		{"echo `id`", true},
		{"cat > file", true},
		{"cmd &", true},
		{"echo #comment", true},
		{"echo ~/file", true},
		{"echo path~/file", false},
		{"echo {name}; rm -rf /", true},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := HasShellMetacharacters(tt.cmd); got != tt.want {
				t.Errorf("HasShellMetacharacters(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestReplaceCommandPlaceholders(t *testing.T) {
	tokens := []string{"echo", "{message}", "{unused}"}
	input := map[string]any{"message": "hello world"}
	got := ReplaceCommandPlaceholders(tokens, input)
	want := []string{"echo", "hello world", "{unused}"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
