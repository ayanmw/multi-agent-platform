/* tool 关于 gemini API 搜索:
 curl --insecure -X POST "https://generativelanguage.googleapis.com/v1beta/interactions"     -H "x-goog-api-key: $GEMINI_API_KEY"     -H "Content-Type: application/json"     -d '{
      "model": "gemini-3.5-flash",
      "input": "Who won the euro 2024?",
      "tools": [{"type": "google_search"}]
    }'

//--insecure 为了避免curl安全证书检测: curl: (35) schannel: next InitializeSecurityContext failed: CRYPT_E_REVOCATION_OFFLINE (0x80092013) -



curl --insecure "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent"     -H 'Content-Type: application/json'     -H 'X-goog-api-key: $GEMINI_API_KEY'     -X POST     -d '{
      "contents": [
        {
          "parts": [
            {
              "text": "Explain how AI works in a few words"
            }
          ]
        }
      ]
    }'
# 返回信息:
{
  "candidates": [
    {
      "content": {
        "parts": [
          {
            "text": "AI learns patterns from vast amounts of data to make predictions and decisions.",
            "thoughtSignature": "EpgLCpULARFNMg+wHwEj6A5Gyo45BcLnUMCj6eB0+Qe6OQ/ctgZvT5kXJpHt6kFTjjkKD2zKUgl0wlUD+Lrmq93M05WPL/QGHkCIgV6Z/DpImj9sETuoE7/Sq3wc7hiWcDr6cCaszDxuB00AZnKJ8GZNLbfAXV8D1rcLk15XbSR+lXra4bWVIk43iXXEL8dCfWyG5IGgdamyXsbl795bvxLaE7RV6Zr7scG23Roc0wGDD5Fxp70MoESgSNlwisYJ39UPAs7x08nMQsD7tDnmSmE7BJwkRJvosKvKVzfOzIhRILF0EXK2iFJGMaAcdFMGj7ToMJc9/QmsVLYrAb/0sd2X+QAhexy5GPIlAWPwTHhTFNaxPrhHo1MzQTu3jErWml4+aW6I4jNhuM7SEjEe5mNvAGnx9GZYzChx0Ri6cRCQrKiYEw4G3r47dW5vUg8JRdzG+gyM0AxFLWUbJpzJTUY+NrCRrN39mEzRSmbE0/zuFt1U0hwS+Ay87UH6E/S8ShySesrIRzVraDKjcyl1QOfuycLIcXAWm+kGoOa21W6aKC9egudeX1W/Nk4gCDVtlQKkvmmk9B6/gJ2W0ybq/qX8dSMe8dGLcKwtvBrXKEwFEgC3AG9bR+/RWl3CvUccDXeCQrmKRYNbcQV7BBwXil8WztDXtT94P8bULC8XyIbxbdeIaLz4Xqj9oqywXULNear+Hi7SUKVEdUnUuihVWYZTT1WxWsOo9FkgELJb08I4v8BU5eqxGQm8+OsCAhmMsptFA5k95SJiBrTqDtvS4msiMcbVD1EH5UlCiC1ZqpQEOsinrlYvObbnsL9kCgvyD/qnJxYu8+/T1YnKplt0oorpA49c1vMIvwUePGgYBckrREC5VLbmmjcvAcb1C+hT/mrc9bm6jxQS4x3d8c8upJP4TpZNyCQTAUCfTkqlM+XPkxK0tDlG8IJ95d6+77tNlq9900NCUVLhAlPNmRQlW84sSTomrT7fdqEu5meONqTAm63SYQPcaDHbXK6giuM8a9T8MNwVTe1xPMURBrF7IwkilYCt4B42HMLTZeHIDdHediO5H+h3qHEx4qNZdIRszeH5eJ4o3ouYYWUfBk1aQvgPi1087bNiP1X4z0dTetZGCQhOLeFOTeA1EIHWTzZxbI1Bc5FoCMOaWVzrIdy8M13yEXrlOJd4+LmY/8MvgYrItSydru10vt5SF5lljwvbVVjlza2aqhXdRpHPTKUoC7bxT1igpOayB/NNfJ9cdyk88tfiilGlY3+S7CMyNb0VFK2WF1yPiTYeTQqk0TMf4bxN9ND4ASToTP274xJZo7SOi9YIcU2OC7zKBvaLUe4EHEiM905cdNzmjnuscyXYJph+nJhB1YACucVBHb8TUSIXIpTE9csJCExXhzf/grUtJjYEubcP6oqHPVLU3e0ab2yaQ9FnfysoLkAFZh8k7Fcv3RtMnvd0nbg+47ryTHEDVSLCg8OAeIh0/2wUFxm0fyAyVp2xaZw6+czcUnVRM7X3Iek9UdqKhENLBuAhg++4bcVFtvxewRPcEl0J2aMR8UYnMr60+NLzECzuDIComJO0vYk1Eu4j0RzU7do65e5j3Nz5rvHtgl+Go5/kxa/QzyYCQxOXC1OpuGpH8iUedFfQhCWZm3ZVVMnrPoeFAMjGsZI2NuA+ojy5N6bgere5QO8OzRpKxlDCj0bqPKukvAzw0ED0vGXGtNvjt6SD/uvZVpzByXZsho/P3piNkzIisYVyBrg5HbWKvQ5Vdm+ObYN//USxviqR7KjcuL3zmcarLgLMDmfWUwClW8n15sU/Z1F+7wEEResOjTMTw2zigyypNT1muTABdCogfGtYKS51jE9sbYI2xgciIuJ16Z/4emQKSbCWSB+QmRnFMH/zlUj4obwFz9h8u9SIsA=="
          }
        ],
        "role": "model"
      },
      "finishReason": "STOP",
      "index": 0
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 8,
    "candidatesTokenCount": 14,
    "totalTokenCount": 390,
    "promptTokensDetails": [
      {
        "modality": "TEXT",
        "tokenCount": 8
      }
    ],
    "thoughtsTokenCount": 368,
    "serviceTier": "standard"
  },
  "modelVersion": "gemini-3.6-flash",
  "responseId": "pt1mauGqKd3kjuMPuKHg8Qw"
}
*/
package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/config/dotenv"
)

func TestWebSearchGeminiInteractions(t *testing.T) {
	var gotBody map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "Spain won Euro 2024, defeating England 2-1 in the final. This victory marks Spain's record fourth European Championship title."}]},
				"groundingMetadata": {
					"webSearchQueries": ["Euro 2024 winner"],
					"groundingChunks": [
						{"web": {"uri": "https://www.aljazeera.com/sports/euro-2024-final", "title": "aljazeera.com"}},
						{"web": {"uri": "https://www.uefa.com/euro2024/news/spain-wins-euro-2024", "title": "uefa.com"}}
					],
					"groundingSupports": [
						{"segment": {"text": "Spain won Euro 2024, defeating England 2-1 in the final."}, "groundingChunkIndices": [0]},
						{"segment": {"text": "This victory marks Spain's record fourth European Championship title."}, "groundingChunkIndices": [1]}
					]
				}
			}]
		}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:       "gemini",
		GeminiAPIKey:   "test-key",
		GeminiEndpoint: srv.URL,
		GeminiModel:    "gemini-2.0-flash",
		HTTPClient:     client,
		Timeout:        5 * time.Second,
	}

	r := NewRegistry()
	r.Register(NewWebSearchTool(cfg))
	res, err := r.Execute("core/web_search", map[string]any{"query": "Who won Euro 2024?", "num_results": 8})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "gemini" {
		t.Fatalf("expected gemini provider, got %v", out["provider"])
	}
	if !strings.HasPrefix(gotPath, "/models/") || !strings.Contains(gotPath, ":generateContent") {
		t.Fatalf("expected generateContent path, got %s", gotPath)
	}
	if gotBody["model"] != nil {
		t.Fatalf("request body must not contain top-level model, got %v", gotBody["model"])
	}
	if !strings.Contains(gotPath, "models/gemini-2.0-flash:generateContent") {
		t.Fatalf("expected model in path, got %s", gotPath)
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, got %v", gotBody["tools"])
	}
	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool object, got %v", tools[0])
	}
	if _, ok := toolMap["google_search"]; !ok {
		t.Fatalf("expected google_search tool, got %v", tools[0])
	}

	text := out["text"].(string)
	if !strings.Contains(text, "Spain won Euro 2024") {
		t.Fatalf("expected answer text, got:\n%s", text)
	}
	if !strings.Contains(text, "aljazeera.com") || !strings.Contains(text, "uefa.com") {
		t.Fatalf("expected citations, got:\n%s", text)
	}
}

func TestWebSearchGeminiNoGroundingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "  AI is a broad field of computer science.  "}]}
			}]
		}`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{
		Provider:       "gemini",
		GeminiAPIKey:   "test-key",
		GeminiEndpoint: srv.URL,
		HTTPClient:     client,
		Timeout:        5 * time.Second,
	}

	text, err := callGemini(context.Background(), cfg, "What is AI?", 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "AI is a broad field") {
		t.Fatalf("expected fallback text, got:\n%s", text)
	}
}

func TestSelectWebSearchProviderGeminiPriority(t *testing.T) {
	if p := selectWebSearchProvider(WebSearchConfig{EnableGemini: true, EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected gemini priority, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{GeminiAPIKey: "key", EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected gemini when key present, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{Provider: "gemini", EnableBrave: true}); p != "gemini" {
		t.Fatalf("expected explicit provider override, got %s", p)
	}
	if p := selectWebSearchProvider(WebSearchConfig{EnableBrave: true}); p != "brave" {
		t.Fatalf("expected brave when no gemini, got %s", p)
	}
}

// TestRealGeminiSearch 使用 .env 中的 GEMINI_API_KEY 对真实 Gemini generateContent
// 端点做一次搜索冒烟测试。默认 Skip，手动运行时加 -run TestRealGeminiSearch。
func TestRealGeminiSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real network test")
	}
	key := dotenv.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	cfg := WebSearchConfig{
		GeminiAPIKey:   key,
		GeminiEndpoint: "https://generativelanguage.googleapis.com/v1beta",
		GeminiModel:    "gemini-2.0-flash",
		Timeout:        25 * time.Second,
		UserAgent:      "multi-agent-platform/0.1.0",
	}
	ctx := t.Context()
	text, err := callGemini(ctx, cfg, "Who won Euro 2024?", 3)
	if err != nil {
		t.Fatalf("gemini search failed: %v", err)
	}
	if text == "" || !strings.Contains(text, "http") || !strings.Contains(text, "Spain") {
		t.Fatalf("expected grounded search results, got:\n%s", text)
	}
	t.Logf("Gemini search result:\n%s", text)
}
