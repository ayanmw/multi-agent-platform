// Package cost provides fallback chain resolution and error classification
// for model fallback strategies.
//
// # Design Rationale
//
// When a primary model fails, the system needs a structured way to:
//  1. Resolve the fallback chain from ModelProfile.FallbackModel links
//  2. Determine whether an error is worth retrying (retryable) or not
//
// Retryable errors include transient failures (network issues, rate limits,
// server errors) that may resolve on a subsequent attempt. Non-retryable
// errors (client errors, auth failures) should propagate immediately.
package cost

import (
	"errors"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// ResolveFallbackChain resolves the fallback chain starting from the primary
// model profile. It follows the FallbackModel links in the registry, collecting
// up to maxDepth profiles (including the primary at position 0).
//
// The returned slice always includes the primary as the first element.
// If the primary has no fallback configured, the slice contains only the primary.
// The chain stops when a model's FallbackModel is empty, not found in the
// registry, or maxDepth is reached (preventing infinite loops from circular refs).
//
// # Example
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
			break // no more fallbacks configured
		}

		next := registry.Get(current.FallbackModel)
		if next == nil {
			break // fallback model not in registry
		}

		// Avoid circular references (already in chain)
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

// IsRetryableError classifies whether an error from an LLM API call is transient
// and worth retrying on a fallback model.
//
// Retryable errors:
//   - Network errors (connection refused, DNS failure, etc.)
//   - HTTP 429 (rate limit exceeded)
//   - HTTP 5xx (server errors)
//   - Context deadline exceeded (timeout)
//   - Errors containing "timeout" or "deadline" in message
//
// Non-retryable errors (return false):
//   - HTTP 400 (bad request)
//   - HTTP 401 (unauthorized / invalid API key)
//   - HTTP 403 (forbidden)
//   - HTTP 404 (not found)
//   - HTTP 422 (validation error)
//   - Any other 4xx client error
//
// When the error is not an HTTP error and doesn't match known transient patterns,
// IsRetryableError returns true as a safe default — unknown errors are assumed
// to be potentially transient.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context deadline exceeded (timeout)
	if errors.Is(err, contextDeadlineExceeded) {
		return true
	}

	// Check for wrapped deadline exceeded
	if strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return true
	}

	// Try to extract HTTP status code from the error message
	// Many OpenAI-compatible clients embed the status code in the error string
	// e.g., "openai: status code 429" or "HTTP 500"
	errMsg := err.Error()

	// Check for common HTTP status patterns in error messages
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

	// Check for common retryable patterns
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

	// Check for non-retryable 4xx patterns
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

	// If we can't classify the error, assume it's retryable (safe default)
	// This ensures transient network flakiness gets a retry attempt
	return true
}

// contextDeadlineExceeded is used for error comparison without importing context
// directly to keep the cost package lightweight.
var contextDeadlineExceeded = errors.New("context deadline exceeded")
