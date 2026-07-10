package llm

import (
	"encoding/json"
)

// BuiltinMockScripts returns the built-in mock scripts used when no dynamic script matches.
// Each script simulates a common agent task shape and can be selected by case_id or keyword.
func BuiltinMockScripts() []MockScript {
	return []MockScript{
		{
			ID:         "builtin:code-gen",
			CaseID:     "code-gen",
			Priority:   100,
			MatchInput: []string{"code", "generate", "file", "program", "function"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_mock_write_file_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "write_file",
								Arguments: mustJSON(map[string]any{"path": "/tmp/mock_gen.go", "content": "package main\n\nfunc main() {\n\t// Mock generated main function\n}\n"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "The file `/tmp/mock_gen.go` has been created with a minimal Go program. Summary: generated a main package and main function as requested.",
				},
			},
		},
		{
			ID:         "builtin:dialogue",
			CaseID:     "dialogue",
			Priority:   100,
			MatchInput: []string{"hello", "hi", "talk", "chat", "question"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: "Hello from mock! I'm running in deterministic mock mode, so this response is generated locally without calling a real LLM.",
				},
			},
		},
		{
			ID:         "builtin:research",
			CaseID:     "research",
			Priority:   100,
			MatchInput: []string{"research", "search", "find", "investigate", "look up"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_mock_run_shell_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "echo mock_research_result"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Research complete. The shell command returned `mock_research_result`; no further external data is available in mock mode.",
				},
			},
		},
		{
			ID:         "builtin:multi-agent",
			CaseID:     "multi-agent",
			Priority:   100,
			MatchInput: []string{"multi", "delegate", "orchestrate", "agents", "team"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: `Subtask dispatch plan (mock): {"subtasks":[{"agent":"researcher","task":"gather context"},{"agent":"writer","task":"draft answer"},{"agent":"reviewer","task":"check quality"}]}. In mock mode these agents will use their own built-in scripts.`,
				},
			},
		},
		{
			ID:         "builtin:long-task",
			CaseID:     "long-task",
			Priority:   100,
			MatchInput: []string{"long", "many", "multiple", "steps"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_mock_run_shell_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "echo long_task_step1"}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_mock_run_shell_2",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "echo long_task_step2"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Long task finished. Steps executed: long_task_step1, long_task_step2. Final summary delivered.",
				},
			},
		},
		{
			ID:         "builtin:tool-error",
			CaseID:     "tool-error",
			Priority:   100,
			MatchInput: []string{"error", "fail", "crash"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_mock_run_shell_error",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "exit 1"}),
							},
						},
					},
				},
			},
		},
	}
}

// mustJSON marshals v to JSON and panics on error. Used for built-in script literals.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
