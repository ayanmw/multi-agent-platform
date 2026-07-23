package llm

import (
	"encoding/json"
)

// BuiltinMockScripts 返回无动态脚本匹配时使用的内置 mock 脚本。
// 每个脚本对应 cases.All() 中的一个内置 case，通过精确 CaseID 匹配被
// 选中（selectScript 中 CaseID 命中 +1000，再叠加 Priority=100，总分
// 1100），稳定胜过 router-classifier（仅 Priority=1000，无 CaseID 命中）。
//
// 设计要点：
//   - 每个 case 脚本的 response 序列还原了该 case 在真实 LLM 下的关键
//     ReAct 行为（tool_call → 最终 text），让 mock 回归脚本能对 status /
//     has_tool / final_result / cost / tokens / 编排事件做出断言。
//   - tool_call 的 arguments 中 session_id/task_id/workdir 由 Engine 在
//     executeTool 内自动用真实值覆盖（engine.go:1803-1820），因此脚本里
//     填占位值即可。
//   - 多 Agent leader 脚本第一个 response 发 dispatch_sub_agent tool_call
//     触发真实 orchestrator 编排（decompose_done/agent_dispatched/
//     agent_completed 事件随之产生），第二个 response 是含验收关键词的
//     最终 text。
//   - tool-error 脚本保留供 keyword（error/fail/crash）命中回退，无对应
//     case，不影响 21 case 的精确匹配。
func BuiltinMockScripts() []MockScript {
	return []MockScript{
		// =====================================================================
		// L1：单 Agent 基线
		// =====================================================================

		// code-gen：write_file 写源码 → 最终 text 总结（has_tool=yes, completed）。
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
							ID:   "call_codegen_write_1",
							Type: "function",
							Function: FunctionCall{
								Name: "write_file",
								Arguments: mustJSON(map[string]any{
									"path":    "fib/fib.go",
									"content": "package fib\n\n// Fib returns the Fibonacci sequence up to n.\nfunc Fib(n int) []int {\n\tseq := make([]int, 0, n)\n\ta, b := 0, 1\n\tfor i := 0; i < n; i++ {\n\t\tseq = append(seq, a)\n\t\ta, b = b, a+b\n\t}\n\treturn seq\n}\n",
								}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Code generation complete. Wrote fib/fib.go containing a Fib(n) function that returns the first n Fibonacci numbers. The file is ready for testing.",
				},
			},
		},

		// dialogue：纯对话无 tool（has_tool=no, completed）。
		{
			ID:         "builtin:dialogue",
			CaseID:     "dialogue",
			Priority:   100,
			MatchInput: []string{"hello", "hi", "talk", "chat", "question"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: "WebSocket maintains a persistent bidirectional connection, while SSE is a one-way server-to-client stream over HTTP. Use WebSocket for real-time chat or collaborative editing; use SSE for live feeds or notifications where the client only needs to listen.\n\n## Key Takeaways\n- WebSocket: full-duplex, lower latency after handshake\n- SSE: simpler, auto-reconnect, HTTP-friendly\n\n**Summary**: pick WebSocket when both sides must push data; pick SSE when the server alone streams updates.",
				},
			},
		},

		// research：write_file 写报告 → 最终 text（has_tool=yes, completed）。
		{
			ID:         "builtin:research",
			CaseID:     "research",
			Priority:   100,
			MatchInput: []string{"research", "search", "find", "investigate", "report"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_research_write_1",
							Type: "function",
							Function: FunctionCall{
								Name: "write_file",
								Arguments: mustJSON(map[string]any{
									"path": "research/ai-agents-2026.md",
									"content": "# AI Agent Frameworks 2026\n\n## Executive Summary\nThree leading agent frameworks compared.\n\n## Background\nAgent frameworks have matured significantly.\n\n## Analysis\nArchitecture, tool support, multi-agent capabilities compared.\n\n## Key Findings\n- Framework A: strong orchestration\n- Framework B: rich tool ecosystem\n- Framework C: best multi-agent primitives\n\n## References\n- https://example.com/framework-a\n- https://example.com/framework-b\n",
								}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Research report saved to research/ai-agents-2026.md with all required sections: Executive Summary, Background, Analysis, Key Findings, and References.",
				},
			},
		},

		// long-task：先 text 描述 → 两个 run_shell step → 最终 text。
		// has_tool=yes（含 run_shell），completed，final 非空。
		{
			ID:         "builtin:long-task",
			CaseID:     "long-task",
			Priority:   100,
			MatchInput: []string{"long", "many", "multiple", "steps"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: "Starting long task setup. First, I will create the directory structure.",
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_longtask_shell_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "mkdir -p task-scheduler/cmd task-scheduler/internal task-scheduler/pkg"}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_longtask_shell_2",
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
					Content: "Long task finished. Steps executed: directory structure created, README and Makefile written. Final summary delivered.",
				},
			},
		},

		// =====================================================================
		// L2：单 Agent + 子系统
		// =====================================================================

		// todo-driven：todo/create → todo/update_status → todo/list → text。
		// has_tool=yes（todo 工具），completed。
		{
			ID:         "builtin:todo-driven",
			CaseID:     "todo-driven",
			Priority:   100,
			MatchInput: []string{"todo", "plan", "task", "subtask"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_todo_create_1",
							Type: "function",
							Function: FunctionCall{
								Name: "todo/create",
								Arguments: mustJSON(map[string]any{
									"session_id": "placeholder",
									"task_id":    "placeholder",
									"title":      "Survey top Go web frameworks",
								}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_todo_status_1",
							Type: "function",
							Function: FunctionCall{
								Name: "todo/update_status",
								Arguments: mustJSON(map[string]any{
									"id":     "todo_mock_1",
									"status": "done",
								}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_todo_list_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "todo/list",
								Arguments: mustJSON(map[string]any{"session_id": "placeholder"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "All todos are completed. Surveyed the top 3 Go web frameworks and marked every todo as done.",
				},
			},
		},

		// web-research：web_search → fetch_url → parse_json → write_file → text。
		// has_tool=yes，completed。
		{
			ID:         "builtin:web-research",
			CaseID:     "web-research",
			Priority:   100,
			MatchInput: []string{"web", "search", "cloud", "market"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_web_search_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "core/web_search",
								Arguments: mustJSON(map[string]any{"query": "top cloud providers market share 2026"}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_fetch_url_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "core/fetch_url",
								Arguments: mustJSON(map[string]any{"url": "https://example.com/cloud-market-2026"}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_write_report_1",
							Type: "function",
							Function: FunctionCall{
								Name: "write_file",
								Arguments: mustJSON(map[string]any{
									"path": "web-research/report.md",
									"content": "# Cloud Market Share 2026\n\nSources:\n- https://example.com/cloud-market-2026\n",
								}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Web research complete. Report saved to web-research/report.md citing https://example.com/cloud-market-2026.",
				},
			},
		},

		// skill-code-helper：skill/list 确认 Skill → write_file → text。
		// has_tool=yes，completed。
		{
			ID:         "builtin:skill-code-helper",
			CaseID:     "skill-code-helper",
			Priority:   100,
			MatchInput: []string{"skill", "reverse", "string", "code"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_skill_list_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "skill/list",
								Arguments: mustJSON(map[string]any{}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_skill_write_1",
							Type: "function",
							Function: FunctionCall{
								Name: "write_file",
								Arguments: mustJSON(map[string]any{
									"path": "skill-demo/reverse.go",
									"content": "package skilldemo\n\n// Reverse returns the rune-reversed form of s.\nfunc Reverse(s string) string {\n\tr := []rune(s)\n\tfor i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {\n\t\tr[i], r[j] = r[j], r[i]\n\t}\n\treturn string(r)\n}\n",
								}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Implemented Reverse in skill-demo/reverse.go following the builtin-code-helper Skill guidance. Usage: Reverse(\"abc\") returns \"cba\".",
				},
			},
		},

		// cron-notify：cron/create → cron/trigger → text。
		// has_tool=yes，completed。
		{
			ID:         "builtin:cron-notify",
			CaseID:     "cron-notify",
			Priority:   100,
			MatchInput: []string{"cron", "schedule", "notify", "timer"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_cron_create_1",
							Type: "function",
							Function: FunctionCall{
								Name: "cron/create",
								Arguments: mustJSON(map[string]any{
									"name":          "hello-notification",
									"schedule_type": "once",
									"once_at":       "2099-01-01T00:00:00Z",
									"action_type":   "notify_session",
									"action_payload": map[string]any{
										"session_id": "placeholder",
										"message":    "Hello from cron",
									},
								}),
							},
						},
					},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_cron_trigger_1",
							Type: "function",
							Function: FunctionCall{
								Name: "cron/trigger",
								Arguments: mustJSON(map[string]any{"id": "cron_mock_1"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "Created cron timer cron_mock_1 and manually triggered it. The notification 'Hello from cron' was delivered to the session.",
				},
			},
		},

		// llm-judge-qa：纯对话开放问答（has_tool=no, completed）。
		{
			ID:         "builtin:llm-judge-qa",
			CaseID:     "llm-judge-qa",
			Priority:   100,
			MatchInput: []string{"monolith", "microservice", "architecture", "trade-off"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: "## Monolith vs Microservice for a Small Team\n\nA monolith keeps deployment, debugging, and inter-service calls simple — ideal when a small team values velocity over independent scaling. Microservices shine when different components need independent deploy cycles or distinct scaling profiles, but they introduce network failure modes and operational overhead.\n\n### When to choose each\n- **Monolith**: small team, unclear domain boundaries, need to ship fast.\n- **Microservice**: clear bounded contexts, independent scaling needs, multiple teams.\n\n**Summary**: for a small team, start with a well-modularized monolith and extract services only when a concrete boundary justifies the cost.",
				},
			},
		},

		// =====================================================================
		// L3：单 Agent + Harness 治理
		// =====================================================================

		// policy-enforcement：write_file(../outside.txt) 撞 PathTraversal
		// 5 个相同 tool_call（MaxSteps=5）→ stepIdx 涨到 5 → max_steps_exceeded。
		// has_tool=yes，status=failed，final 空（failed 天然清空 final_result）。
		{
			ID:         "builtin:policy-enforcement",
			CaseID:     "policy-enforcement",
			Priority:   100,
			MatchInput: []string{"policy", "outside", "blocked", "scope"},
			Responses: []MockResponse{
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_policy_write_1", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "../outside.txt", "content": "blocked"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_policy_write_2", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "../outside.txt", "content": "blocked"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_policy_write_3", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "../outside.txt", "content": "blocked"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_policy_write_4", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "../outside.txt", "content": "blocked"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_policy_write_5", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "../outside.txt", "content": "blocked"})},
				}}},
			},
		},

		// approval-flow：run_shell(echo) → text。AllowShellDangerous=true 且
		// echo 非高风险命令，直接执行 exit 0。has_tool=yes, completed, final 非空。
		{
			ID:         "builtin:approval-flow",
			CaseID:     "approval-flow",
			Priority:   100,
			MatchInput: []string{"approval", "sensitive", "echo", "confirm"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{
						{
							Idx:  0,
							ID:   "call_approval_shell_1",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_shell",
								Arguments: mustJSON(map[string]any{"command": "echo sensitive-action-confirmed"}),
							},
						},
					},
				},
				{
					Type:    MockResponseText,
					Content: "The sensitive command was approved and executed successfully. Output: sensitive-action-confirmed.",
				},
			},
		},

		// max-steps-exhaustion：4 个 write_file tool_call（MaxSteps=3）。
		// stepIdx: think0→tool1(=1)→think1→tool2(=2)→think2→tool3(=3)
		// → 循环条件 stepIdx(3)<MaxSteps(3) false → max_steps_exceeded。
		// has_tool=yes，status=failed，final 空。
		{
			ID:         "builtin:max-steps-exhaustion",
			CaseID:     "max-steps-exhaustion",
			Priority:   100,
			MatchInput: []string{"poem", "verse", "max", "steps"},
			Responses: []MockResponse{
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_maxsteps_write_1", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "verse_1.txt", "content": "verse one"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_maxsteps_write_2", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "verse_2.txt", "content": "verse two"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_maxsteps_write_3", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "verse_3.txt", "content": "verse three"})},
				}}},
				{Type: MockResponseToolCall, ToolCalls: []ToolCall{{
					Idx: 0, ID: "call_maxsteps_write_4", Type: "function",
					Function: FunctionCall{Name: "write_file", Arguments: mustJSON(map[string]any{"path": "verse_4.txt", "content": "verse four"})},
				}}},
			},
		},

		// context-compression：纯 text 产出长内容再压缩成 3 条要点。
		// has_tool=no（EXP_TOOL=no），completed。
		// Compressor 按 turn/token 阈值触发，单 case 不会真压缩，但 completed
		// 终态与 final 非空即可 PASS。
		{
			ID:         "builtin:context-compression",
			CaseID:     "context-compression",
			Priority:   100,
			MatchInput: []string{"compress", "facts", "summary", "go"},
			Responses: []MockResponse{
				{
					Type:    MockResponseText,
					Content: "Generated 20 facts about Go, then compressed them into 3 key bullets:\n\n- Go is statically typed with a focus on simplicity and fast compilation.\n- Goroutines and channels enable lightweight concurrency.\n- The standard library is comprehensive and production-ready.\n\nCompressed summary saved to compression/summary.md.",
				},
			},
		},

		// checkpoint-resume：3 次 write_file 更新进度文件 → text。
		// has_tool=yes，completed。
		{
			ID:         "builtin:checkpoint-resume",
			CaseID:     "checkpoint-resume",
			Priority:   100,
			MatchInput: []string{"checkpoint", "resume", "progress", "pause"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_ckpt_write_1", Type: "function",
						Function: FunctionCall{
							Name: "write_file",
							Arguments: mustJSON(map[string]any{
								"path":    "checkpoint/steps.md",
								"content": "# Checkpoint Progress\n\n- step 1: initialized\n",
							}),
						},
					}},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_ckpt_write_2", Type: "function",
						Function: FunctionCall{
							Name: "write_file",
							Arguments: mustJSON(map[string]any{
								"path":    "checkpoint/steps.md",
								"content": "# Checkpoint Progress\n\n- step 1: initialized\n- step 2: processed\n",
							}),
						},
					}},
				},
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_ckpt_write_3", Type: "function",
						Function: FunctionCall{
							Name: "write_file",
							Arguments: mustJSON(map[string]any{
								"path":    "checkpoint/steps.md",
								"content": "# Checkpoint Progress\n\n- step 1: initialized\n- step 2: processed\n- step 3: completed\n",
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Checkpoint resume complete. All three progress entries written to checkpoint/steps.md, including step 3. Task marked complete.",
				},
			},
		},

		// =====================================================================
		// L4：多 Agent 静态编排（leader = root agent）
		// 每个 leader 脚本：[0] dispatch_sub_agent tool_call 触发真实编排；
		// [1] 含验收关键词的最终 text。
		// =====================================================================

		// multi-agent（legacy）：dispatch 1 worker → text。
		// has_tool=yes（dispatch_sub_agent），completed。
		// legacy case 不要求 child_steps，但走真编排可顺便满足事件断言。
		{
			ID:         "builtin:multi-agent",
			CaseID:     "multi-agent",
			Priority:   100,
			MatchInput: []string{"multi", "delegate", "orchestrate", "agents", "team"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_legacy_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Simulate the legacy multi-role workflow via a real sub-agent dispatch.",
								"strategy": "parallel",
								"agents": []map[string]any{{
									"agent_id":      "agent_implementer",
									"system_prompt": "You are an implementer. Produce a concise design summary.",
									"input":         "Summarize a simple REST API rate limiter design.",
								}},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Legacy multi-role simulation complete. The dispatched implementer sub-agent produced the rate limiter design summary. Final review consolidated.",
				},
			},
		},

		// multi-agent-parallel：3 worker 并行 → final 含 "review"。
		{
			ID:         "builtin:multi-agent-parallel",
			CaseID:     "multi-agent-parallel",
			Priority:   100,
			MatchInput: []string{"parallel", "review", "scalability", "security"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_parallel_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Review the REST API design across three dimensions in parallel.",
								"strategy": "parallel",
								"agents": []map[string]any{
									{"agent_id": "agent_reviewer", "system_prompt": "You are a scalability reviewer.", "input": "Review scalability of a simple REST API for a task scheduler."},
									{"agent_id": "agent_security", "system_prompt": "You are a security reviewer.", "input": "Review security of a simple REST API for a task scheduler."},
									{"agent_id": "agent_usability", "system_prompt": "You are a usability reviewer.", "input": "Review usability of a simple REST API for a task scheduler."},
								},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Parallel review complete. Combined the scalability, security, and usability findings into a single review summary for the task scheduler REST API.",
				},
			},
		},

		// multi-agent-sequential：researcher → writer 顺序 → final 含 "report"。
		{
			ID:         "builtin:multi-agent-sequential",
			CaseID:     "multi-agent-sequential",
			Priority:   100,
			MatchInput: []string{"sequential", "researcher", "writer", "serverless"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_sequential_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Run researcher then writer sequentially so writer builds on research findings.",
								"strategy": "sequential",
								"agents": []map[string]any{
									{"agent_id": "agent_researcher", "system_prompt": "You are a researcher.", "input": "Research the pros and cons of serverless vs containers."},
									{"agent_id": "agent_writer", "system_prompt": "You are a technical writer.", "input": "Write a report based on the research findings."},
								},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Sequential orchestration complete. The researcher gathered serverless vs container trade-offs and the writer turned them into a final report.",
				},
			},
		},

		// multi-agent-dag：analyst → designer → implementer pipeline → final 含 "implement"。
		{
			ID:         "builtin:multi-agent-dag",
			CaseID:     "multi-agent-dag",
			Priority:   100,
			MatchInput: []string{"dag", "pipeline", "analyst", "implementer"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_dag_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Run a 3-stage DAG pipeline: analyst -> designer -> implementer.",
								"strategy": "pipeline",
								"agents": []map[string]any{
									{"agent_id": "agent_analyst", "system_prompt": "You are an analyst.", "input": "Analyze requirements for a tiny URL shortener service."},
									{"agent_id": "agent_designer", "system_prompt": "You are a designer.", "input": "Design the architecture for a tiny URL shortener based on the analysis."},
									{"agent_id": "agent_implementer", "system_prompt": "You are an implementer.", "input": "Implement the tiny URL shortener based on the design."},
								},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "DAG pipeline complete. Analyst specified requirements, designer produced the architecture, and the implementer delivered the URL shortener implementation.",
				},
			},
		},

		// =====================================================================
		// L5：多 Agent 动态编排
		// =====================================================================

		// multi-agent-leader-dispatch：leader 派 1 worker → final 含 "moving average"。
		{
			ID:         "builtin:multi-agent-leader-dispatch",
			CaseID:     "multi-agent-leader-dispatch",
			Priority:   100,
			MatchInput: []string{"leader", "dispatch", "delegate", "moving"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_leader_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Delegate the Python moving-average implementation to the coder worker.",
								"strategy": "parallel",
								"agents": []map[string]any{{
									"agent_id":      "agent_coder",
									"system_prompt": "You are a Python coder.",
									"input":         "Write a Python function that calculates moving averages.",
								}},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Delegation complete. The coder worker returned a Python moving average function plus a short explanation of the sliding window approach.",
				},
			},
		},

		// multi-agent-review：writer → reviewer 顺序 → final 含 "verdict"。
		{
			ID:         "builtin:multi-agent-review",
			CaseID:     "multi-agent-review",
			Priority:   100,
			MatchInput: []string{"review", "writer", "reviewer", "verdict"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_review_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Writer drafts the design then reviewer critiques it sequentially.",
								"strategy": "sequential",
								"agents": []map[string]any{
									{"agent_id": "agent_writer", "system_prompt": "You are a design writer.", "input": "Write a short design doc adding retry logic to the task scheduler."},
									{"agent_id": "agent_reviewer", "system_prompt": "You are a design reviewer.", "input": "Review the retry-logic design doc and provide feedback."},
								},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Review loop complete. The writer produced the retry-logic design, the reviewer gave feedback, and the leader verdict is: approve with minor revisions.",
				},
			},
		},

		// multi-agent-fault-tolerance：primary + fallback 并行 → final 含 "fallback"。
		{
			ID:         "builtin:multi-agent-fault-tolerance",
			CaseID:     "multi-agent-fault-tolerance",
			Priority:   100,
			MatchInput: []string{"fault", "tolerance", "fallback", "resilient"},
			Responses: []MockResponse{
				{
					Type: MockResponseToolCall,
					ToolCalls: []ToolCall{{
						Idx: 0, ID: "call_ft_dispatch_1", Type: "function",
						Function: FunctionCall{
							Name: "dispatch_sub_agent",
							Arguments: mustJSON(map[string]any{
								"reason":   "Attempt primary optimization with a fallback baseline in parallel.",
								"strategy": "parallel",
								"agents": []map[string]any{
									{"agent_id": "agent_primary", "system_prompt": "You are a primary optimizer.", "input": "Attempt a complex optimization for the scheduler."},
									{"agent_id": "agent_fallback", "system_prompt": "You are a fallback baseline producer.", "input": "Produce a simple FIFO baseline answer for the scheduler."},
								},
							}),
						},
					}},
				},
				{
					Type:    MockResponseText,
					Content: "Fault-tolerant dispatch complete. The primary worker's result was used; the fallback baseline was kept ready in case of degradation. No crash occurred.",
				},
			},
		},

		// =====================================================================
		// keyword 回退脚本（无对应 case，供真实 LLM 模式或 error 类输入命中）
		// =====================================================================

		// tool-error：keyword（error/fail/crash）命中回退，模拟一次失败 tool call。
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

// mustJSON 将 v 序列化为 JSON，出错则 panic。用于内置脚本字面量。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
