// Package cost 提供 model fallback 策略的 fallback chain 解析与错误分类。
//
// # 设计理由
//
// 当主模型失败时，系统需要一种结构化的方式来：
//  1. 从 ModelProfile.FallbackModel 链接解析 fallback chain
//  2. 判断一个错误是否值得重试（retryable）
//
// Retryable 错误包括可能在下次尝试时恢复的瞬时故障（网络问题、rate limit、
// 服务器错误）。Non-retryable 错误（客户端错误、认证失败）应立即传播。
package cost

import (
	"errors"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// ResolveFallbackChain 从主 model profile 起解析 fallback chain。它沿 registry 中的
// FallbackModel 链接跟进，最多收集 maxDepth 个 profile（含位于位置 0 的主模型）。
//
// 返回的 slice 始终以主模型作为第一个元素。
// 若主模型未配置 fallback，则 slice 仅包含主模型。
// 当某个模型的 FallbackModel 为空、在 registry 中找不到，或达到 maxDepth 时，
// chain 停止（从而防止循环引用导致的无限循环）。
//
// # 示例
//
//	registry := llm.NewModelRegistry()
//	registry.Register(&llm.ModelProfile{Name: "pro", FallbackModel: "flash"})
//	registry.Register(&llm.ModelProfile{Name: "flash", FallbackModel: ""})
//	chain := ResolveFallbackChain(registry, registry.Get("pro"), 3)
//	// chain = [pro, flash]
func ResolveFallbackChain(registry *llm.ModelRegistry, primary *llm.ModelProfile, maxDepth int) []*llm.ModelProfile {
	if registry == nil || primary == nil || maxDepth <= 0 {
		return nil
	}

	chain := []*llm.ModelProfile{primary}
	current := primary

	for len(chain) < maxDepth {
		if current.FallbackModel == "" {
			break // 没有更多 fallback 配置
		}

		next := registry.Get(current.FallbackModel)
		if next == nil {
			break // fallback model 不在 registry 中
		}

		// 避免循环引用（已在 chain 中）
		alreadyInChain := false
		for _, existing := range chain {
			if existing.Name == next.Name {
				alreadyInChain = true
				break
			}
		}
		if alreadyInChain {
			break
		}

		chain = append(chain, next)
		current = next
	}

	return chain
}

// IsRetryableError 判断一个来自 LLM API 调用的错误是否为瞬时错误，值得在 fallback
// model 上重试。
//
// Retryable 错误：
//   - 网络错误（connection refused、DNS 失败等）
//   - HTTP 429（rate limit exceeded）
//   - HTTP 5xx（服务器错误）
//   - Context deadline exceeded（超时）
//   - 消息中包含 "timeout" 或 "deadline" 的错误
//
// Non-retryable 错误（返回 false）：
//   - HTTP 400（bad request）
//   - HTTP 401（unauthorized / 无效 API key）
//   - HTTP 403（forbidden）
//   - HTTP 404（not found）
//   - HTTP 422（validation error）
//   - 其它任何 4xx 客户端错误
//
// 当错误不是 HTTP 错误且不匹配已知的瞬时模式时，IsRetryableError 作为安全默认返回
// true——未知错误被假定为可能是瞬时的。
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// 检查 context deadline exceeded（超时）
	if errors.Is(err, contextDeadlineExceeded) {
		return true
	}

	// 检查被包装的 deadline exceeded
	if strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return true
	}

	// 尝试从错误消息中提取 HTTP 状态码
	// 许多 OpenAI 兼容客户端将状态码嵌入错误字符串中，
	// 例如 "openai: status code 429" 或 "HTTP 500"
	errMsg := err.Error()

	// 检查错误消息中常见的 HTTP 状态模式
	for _, statusPattern := range []string{
		"status code 429",
		"status code 500",
		"status code 502",
		"status code 503",
		"status code 504",
		"HTTP 429",
		"HTTP 500",
		"HTTP 502",
		"HTTP 503",
		"HTTP 504",
		"429 Too Many Requests",
		"500 Internal Server Error",
		"502 Bad Gateway",
		"503 Service Unavailable",
		"504 Gateway Timeout",
	} {
		if strings.Contains(errMsg, statusPattern) {
			return true
		}
	}

	// 检查常见的 retryable 模式
	retryablePatterns := []string{
		"rate limit",
		"too many requests",
		"server error",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		"connection reset",
		"connection refused",
		"no such host",
		"i/o timeout",
		"temporary failure",
		"try again",
	}
	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errMsg), pattern) {
			return true
		}
	}

	// 检查 non-retryable 的 4xx 模式
	nonRetryablePatterns := []string{
		"status code 400",
		"status code 401",
		"status code 403",
		"status code 404",
		"status code 405",
		"status code 409",
		"status code 422",
		"status code 451",
		"HTTP 400",
		"HTTP 401",
		"HTTP 403",
		"HTTP 404",
		"HTTP 405",
		"HTTP 409",
		"HTTP 422",
		"HTTP 451",
		"invalid api key",
		"unauthorized",
		"forbidden",
		"not found",
		"bad request",
		"validation error",
		"model not found",
		"invalid request",
	}
	for _, pattern := range nonRetryablePatterns {
		if strings.Contains(strings.ToLower(errMsg), pattern) {
			return false
		}
	}

	// 如果无法分类该错误，则假定为 retryable（安全默认值）
	// 这确保了瞬时网络抖动能获得重试机会
	return true
}

// contextDeadlineExceeded 用于错误比较，但不直接导入 context，
// 以保持 cost 包的轻量。
var contextDeadlineExceeded = errors.New("context deadline exceeded")
