package runtime

import "github.com/ayanmw/multi-agent-platform/internal/llm"

// llmProviderAdapter 把 engine 侧真实的 llm.Provider 适配为 tool 包的
// tool.LLMProvider 小接口，避免 tool 包直接依赖 llm 包造成循环引用。
type llmProviderAdapter struct {
	provider llm.Provider
}

// Chat 实现 tool.LLMProvider，将任意请求断言为 *llm.ChatRequest 后转调
// 真实 provider 的非流式 Chat。web_research 等 tool 内部只做一次性总结，
// 不需要 streaming。
func (a *llmProviderAdapter) Chat(req any) (any, error) {
	chatReq, ok := req.(llm.ChatRequest)
	if !ok {
		// 也支持指针形式，方便调用方按需传递。
		if ptr, ok2 := req.(*llm.ChatRequest); ok2 {
			chatReq = *ptr
		} else {
			return nil, nil
		}
	}
	resp, err := a.provider.Chat(chatReq)
	if err != nil {
		return nil, err
	}
	// 返回 *llm.ChatResponse，tool 内部提取 Content 与 Usage。
	return resp, nil
}
