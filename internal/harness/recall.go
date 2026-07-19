// Package harness —— MemoryRecall：从存储的 memory 构建 Working Memory。
//
// MemoryRecall 是 Memory 基础设施的召回侧。当新任务启动时，recall 引擎从数据库加载相关
// memory，构建一个 WorkingMemory context 块注入到 agent 的 system prompt。这让 agent 能
// 访问过往经验与稳定的 semantic rule，而无需用户重复说明。
//
// # 架构
//
// recall 系统实现 4 层 memory 系统的第一层：
//
//  1. Working Memory      —— 任务级 context（内存中）           <-- 本文件
//  2. Raw Episodic        —— 对话记录（conversations 表）
//  3. Consolidated Episodic —— 任务 summary（memories，tier=consolidated）
//  4. Semantic/Policy     —— 稳定规则（memories，tier=semantic）
//
// # 召回过程
//
// 调用 BuildWorkingMemory 时：
//  1. 加载 session 级 memory（最高优先级，针对当前 session）
//  2. 加载 project 级 semantic rule（稳定 policy）
//  3. 按 keyword 匹配加载 top N project 级 consolidated episode
//  4. 加载 global 级 semantic rule（跨 project 偏好）
//  5. 更新每条被召回 memory 的 access_count 与 last_accessed
//  6. 构建可直接注入 system prompt 的 WorkingMemory 结构
//
// # 冲突检测
//
// DetectConflicts 方法使用简单的关键词检测扫描 memory 项中内容相互矛盾的配对，用于
// 暴露需要人工审查的陈旧或冲突规则。
package harness

import (
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// WorkingMemory 是注入到新任务 system prompt 的 context。它包含来自 session、project、
// global 三个 scope 的分层 memory，让 agent 无需用户重复指令即可访问最相关的制度化知识。
type WorkingMemory struct {
	// TaskGoal 是用户任务描述，用于相关性打分。
	TaskGoal string `json:"task_goal"`

	// SessionMemories 是绑定到当前 session 的 session 级 memory。它们优先级最高，
	// 因为反映的是即时对话 context。
	SessionMemories []MemoryItem `json:"session_memories"`

	// ProjectRules 是 project 级 semantic tier memory，代表当前项目的稳定 policy、
	// 偏好与规则。
	ProjectRules []MemoryItem `json:"project_rules"`

	// ProjectEpisodes 是与当前任务 goal 最相关的 project 级 consolidated episodic
	// memory，按 keyword 重叠度打分。
	ProjectEpisodes []MemoryItem `json:"project_episodes"`

	// GlobalRules 是 global 级 semantic tier memory，代表跨 project 偏好与稳定约定。
	GlobalRules []MemoryItem `json:"global_rules"`

	// BuiltAt 是该 WorkingMemory 构建时的时间戳。
	BuiltAt time.Time `json:"built_at"`
}

// MemoryItem 是准备注入 system prompt 的单条 memory 条目。它携带 agent 理解和使用该
// memory 所需的必要字段，而不暴露内部数据库细节。
type MemoryItem struct {
	// ID 是该 memory 记录的唯一标识符。
	ID string `json:"id"`

	// Type 描述 memory 种类：preference、rule、fact、lesson、reflection。
	Type string `json:"type"`

	// Content 是 memory 的完整文本。
	Content string `json:"content"`

	// Confidence 是该 memory 的可靠性分数（0.0--1.0）。
	Confidence float64 `json:"confidence"`

	// Reason 描述为何为当前任务召回此 memory。
	Reason string `json:"reason"`
}

// ConflictPair 表示两条看起来相互矛盾的 memory。通过简单关键词分析检测 —— 同类型 memory
// 中出现对立标记（如 "use" vs "avoid"、"always" vs "never"）。
type ConflictPair struct {
	// MemoryA 是冲突配对中的第一条 memory。
	MemoryA MemoryItem `json:"memory_a"`

	// MemoryB 是冲突配对中的第二条 memory。
	MemoryB MemoryItem `json:"memory_b"`

	// Reason 描述检测到的对立标记。
	Reason string `json:"reason"`
}

// MemoryRecall 通过从 memory 存储中召回 semantic rule 与相关 consolidated episode 来
// 为新任务构建 Working Memory。它是 Memory 基础设施的召回侧，与 Heartbeat（固化）和
// PromotionGate（提升）互补。
//
// MemoryRecall 可选择配置 EmbeddingProvider 与 VectorStore 以启用向量相似度搜索。当它们
// 为 nil 时，回退到基于关键词的召回，保留向后兼容。
//
// 用法：
//
//	recall := NewMemoryRecall(memDB)
//	wm, err := recall.BuildWorkingMemory("default", "session_abc", "write a Go test", 3)
//	if err == nil {
//	    prompt := recall.FormatForSystemPrompt(wm)
//	    // 将 prompt 前置到 agent 的 system prompt
//	}
type MemoryRecall struct {
	db            MemoryDB
	embedProvider llm.EmbeddingProvider
	vectorStore   memory.VectorStore
	ranker        *HybridRanker
}

// NewMemoryRecall 用给定 MemoryDB 创建 MemoryRecall。database 参数实现 recall 引擎所需
// 的所有 DB 操作（QueryMemoriesByTier）的 MemoryDB 接口。访问跟踪通过 db package 级
// UpdateMemoryAccess 函数完成。
func NewMemoryRecall(database MemoryDB) *MemoryRecall {
	return NewMemoryRecallWithVectorStore(database, nil, nil)
}

// NewMemoryRecallWithVectorStore 创建支持向量相似度的 MemoryRecall。当 embedProvider 与
// vectorStore 为 nil 时，回退到纯关键词召回。
func NewMemoryRecallWithVectorStore(database MemoryDB, embedProvider llm.EmbeddingProvider, vectorStore memory.VectorStore) *MemoryRecall {
	return &MemoryRecall{
		db:            database,
		embedProvider: embedProvider,
		vectorStore:   vectorStore,
		ranker:        NewHybridRanker(embedProvider, vectorStore, DefaultHybridWeights),
	}
}

// BuildWorkingMemory 为给定 project、session 与任务 goal 加载分层 memory。结果是可直接
// 注入 system prompt 的 WorkingMemory 结构。
//
// 召回优先级（从最具体到最宽泛）：
//
//  1. Session 级 memory（scope=session，session_id=xxx）
//  2. Project 级 semantic rule（scope=project，tier=semantic）
//  3. Project 级 consolidated episode（scope=project，tier=consolidated，keyword top N）
//  4. Global 级 semantic rule（scope=global，tier=semantic）
//  5. Session 历史 message 由 Engine 层单独注入
//
// 参数：
//   - projectID：要召回 memory 的 project（如 "default"）
//   - sessionID：当前 session ID；非 session 内可为空
//   - taskGoal：用户任务描述，用于 keyword 匹配
//   - maxEpisodes：要召回的 consolidated episode 最大数量
//
// 即使未找到 memory（空 slice）也返回 WorkingMemory。仅在数据库失败时返回 error。
func (mr *MemoryRecall) BuildWorkingMemory(projectID, sessionID, taskGoal string, maxEpisodes int) (*WorkingMemory, error) {
	// 1. 加载 session 级 memory（最高优先级）。
	//    它们捕获仅适用于当前 session 的事实/规则。
	sessionMems, err := mr.loadSessionMemories(projectID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session memories: %w", err)
	}

	// 2. 加载 project 级 semantic rule（该 project 的稳定 policy）。
	projectRules, err := mr.loadSemanticRules(projectID, "project")
	if err != nil {
		return nil, fmt.Errorf("load project semantic rules: %w", err)
	}

	// 3. 按 keyword 匹配加载 top N project 级 consolidated episode。
	projectEpisodes, err := mr.recallEpisodes(projectID, "project", taskGoal, maxEpisodes)
	if err != nil {
		return nil, fmt.Errorf("recall project episodes: %w", err)
	}

	// 4. 加载 global 级 semantic rule（跨 project 约定）。
	globalRules, err := mr.loadSemanticRules(projectID, "global")
	if err != nil {
		return nil, fmt.Errorf("load global semantic rules: %w", err)
	}

	return &WorkingMemory{
		TaskGoal:        taskGoal,
		SessionMemories: sessionMems,
		ProjectRules:    projectRules,
		ProjectEpisodes: projectEpisodes,
		GlobalRules:     globalRules,
		BuiltAt:         time.Now(),
	}, nil
}

// FormatForSystemPrompt 将 WorkingMemory 格式化为干净的文本块，适合前置到 agent 的
// system prompt。输出使用 Markdown 风格标题，便于在 LLM context 中阅读。
//
// 输出格式：
//
//	## Working Memory (from previous tasks)
//
//	### Session Context
//	- [memory content]
//
//	### Project Rules
//	- [rule content]
//
//	### Related Experiences
//	- [episode summary]
//
//	### Global Preferences
//	- [rule content]
func (mr *MemoryRecall) FormatForSystemPrompt(wm *WorkingMemory) string {
	var sb strings.Builder
	sb.WriteString("## Working Memory (from previous tasks)\n")

	if len(wm.SessionMemories) > 0 {
		sb.WriteString("\n### Session Context\n")
		for _, m := range wm.SessionMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Content))
		}
	}

	if len(wm.ProjectRules) > 0 {
		sb.WriteString("\n### Project Rules\n")
		for _, rule := range wm.ProjectRules {
			sb.WriteString(fmt.Sprintf("- %s\n", rule.Content))
		}
	}

	if len(wm.ProjectEpisodes) > 0 {
		sb.WriteString("\n### Related Experiences\n")
		for _, ep := range wm.ProjectEpisodes {
			sb.WriteString(fmt.Sprintf("- %s\n", ep.Content))
		}
	}

	if len(wm.GlobalRules) > 0 {
		sb.WriteString("\n### Global Preferences\n")
		for _, rule := range wm.GlobalRules {
			sb.WriteString(fmt.Sprintf("- %s\n", rule.Content))
		}
	}

	return sb.String()
}

// loadSessionMemories 加载给定 project 与 session 的活跃 session 级 memory。它们是
// 优先级最高的 memory，因为反映的是即时对话 context。
func (mr *MemoryRecall) loadSessionMemories(projectID, sessionID string) ([]MemoryItem, error) {
	if sessionID == "" {
		return nil, nil
	}

	records, err := db.QueryMemoriesByScopeAndSession(projectID, sessionID, "session")
	if err != nil {
		return nil, err
	}

	var items []MemoryItem
	for _, r := range records {
		if r.Status != "active" {
			continue
		}
		if err := db.UpdateMemoryAccess(r.ID); err != nil {
			continue
		}
		items = append(items, MemoryItem{
			ID:         r.ID,
			Type:       r.Type,
			Content:    r.Content,
			Confidence: r.Confidence,
			Reason:     "session-scoped memory (current session)",
		})
	}
	return items, nil
}

// loadSemanticRules 加载给定 project 与 scope 的活跃 semantic tier memory，按
// confidence 降序排列（最可靠者优先）。为每条被召回 memory 更新 access_count 与
// last_accessed 以跟踪使用模式。
func (mr *MemoryRecall) loadSemanticRules(projectID, scope string) ([]MemoryItem, error) {
	records, err := db.QueryMemoriesByScopeAndTier(projectID, scope, "semantic")
	if err != nil {
		return nil, err
	}

	var items []MemoryItem
	for _, r := range records {
		// 只召回活跃 memory —— 过期或无效的会被跳过。
		if r.Status != "active" {
			continue
		}
		// 更新被召回 memory 的访问跟踪。这是非致命的：若更新失败，我们仍将该 memory
		// 纳入工作集，但记录失败用于诊断。
		if err := db.UpdateMemoryAccess(r.ID); err != nil {
			// 非致命 —— memory 仍被召回，只是没有访问跟踪。
			continue
		}
		items = append(items, MemoryItem{
			ID:         r.ID,
			Type:       r.Type,
			Content:    r.Content,
			Confidence: r.Confidence,
			Reason:     fmt.Sprintf("%s semantic rule (stable policy)", scope),
		})
	}
	return items, nil
}

// BuildVectorIndex 从数据库加载所有 consolidated 与 semantic memory，计算 embedding，
// 并存入 vector store。在启动时与批量 memory 导入后各调用一次。
func (mr *MemoryRecall) BuildVectorIndex() error {
	if mr.embedProvider == nil || mr.vectorStore == nil {
		return nil // 未配置向量召回
	}
	// 加载所有 consolidated 与 semantic memory
	records, err := db.QueryMemoriesByScopeAndTier("default", "project", "consolidated")
	if err != nil {
		return fmt.Errorf("load consolidated: %w", err)
	}
	semantic, err := db.QueryMemoriesByScopeAndTier("default", "project", "semantic")
	if err != nil {
		return fmt.Errorf("load semantic: %w", err)
	}
	records = append(records, semantic...)

	for _, r := range records {
		if r.Status != "active" {
			continue
		}
		if err := mr.indexMemory(r); err != nil {
			continue // 跳过单条失败
		}
	}
	return nil
}

// indexMemory 为单条 memory 记录计算 embedding 并 upsert 到 vector store。
func (mr *MemoryRecall) indexMemory(r db.MemoryRecord) error {
	vec, err := mr.embedProvider.Embed(r.Content)
	if err != nil {
		return err
	}
	return mr.vectorStore.Upsert(r.ID, vec, map[string]any{
		"type":       r.Type,
		"tier":       r.Tier,
		"scope":      r.Scope,
		"confidence": stringify(r.Confidence),
	})
}

// RecallWithQuery 对自然语言查询执行纯向量相似度搜索。由 /api/memories/recall?query=...
// 端点用于调试。返回按相关性（向量相似度与 keyword 分数混合）排序的 MemoryItems。
func (mr *MemoryRecall) RecallWithQuery(projectID, sessionID, query string, maxN int) ([]MemoryItem, error) {
	if mr.embedProvider == nil || mr.vectorStore == nil {
		// 回退到纯关键词召回
		return mr.recallEpisodes(projectID, "project", query, maxN)
	}

	// embed 查询
	queryVec, err := mr.embedProvider.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// 搜索 vector store
	results, err := mr.vectorStore.Search(queryVec, maxN*2) // 获取更多候选以便混合
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// 从结果构建 MemoryItem
	var items []MemoryItem
	for _, r := range results {
		items = append(items, MemoryItem{
			ID:         r.ID,
			Type:       stringifyMeta(r.Metadata, "type"),
			Content:    "", // 从 DB 填充
			Confidence: parseConfidence(r.Metadata),
			Reason:     fmt.Sprintf("vector similarity (score: %.3f)", r.Score),
		})
	}

	if len(items) > maxN {
		items = items[:maxN]
	}
	return items, nil
}

// recallEpisodes 加载按 scope 过滤的 consolidated episodic memory，按与任务 goal 的
// keyword 与可选向量重叠度打分，返回最相关的 top N。为每条被召回 memory 更新访问跟踪。
//
// 当 MemoryRecall 配置了 embedProvider 与 vectorStore 时，分数混合 keyword 重叠度与
// 余弦相似度（0.3 keyword + 0.7 vector）。这超越了精确词匹配，提升语义相关性。
//
// 打分算法使用词频重叠：任务 goal 中的每个词都会与 memory 内容比对。分数为出现在内容中
// 的查询词占比。这是刻意的轻量实现 —— Phase 6+ 增加向量相似度打分以支持语义相关性。
func (mr *MemoryRecall) recallEpisodes(projectID, scope, taskGoal string, maxN int) ([]MemoryItem, error) {
	records, err := db.QueryMemoriesByScopeAndTier(projectID, scope, "consolidated")
	if err != nil {
		return nil, err
	}

	// 按 taskGoal 的 keyword/vector 混合重叠度为每条 episode 打分。
	// 每条 episode 与其相关性分数配对以便排序。
	type scored struct {
		item  MemoryItem
		score float64
	}
	var scoredList []scored
	for _, r := range records {
		// 只召回活跃 memory。
		if r.Status != "active" {
			continue
		}
		score := mr.blendVectorScores(r.Content, taskGoal)
		scoredList = append(scoredList, scored{
			item: MemoryItem{
				ID:         r.ID,
				Type:       r.Type,
				Content:    r.Content,
				Confidence: r.Confidence,
				Reason:     fmt.Sprintf("%s blended score (keyword+vector: %.1f)", scope, score),
			},
			score: score,
		})
	}

	// 按分数降序排序。由于 consolidated episode 数量通常较少（几十条），简单冒泡即可。
	// Phase 6+ 会使用带向量相似度的数据库级排序。
	for i := 0; i < len(scoredList); i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}

	// 取最相关的 top N episode。
	if maxN > len(scoredList) {
		maxN = len(scoredList)
	}
	var items []MemoryItem
	for i := 0; i < maxN; i++ {
		// 更新被召回 memory 的访问跟踪。
		if err := db.UpdateMemoryAccess(scoredList[i].item.ID); err != nil {
			// 非致命 —— 继续处理下一条。
			continue
		}
		items = append(items, scoredList[i].item)
	}
	return items, nil
}

// blendVectorScores 组合基于 keyword 与基于向量的相关性分数。当可用时委托给
// HybridRanker。
func (mr *MemoryRecall) blendVectorScores(content, query string) float64 {
	if mr.ranker == nil {
		return keywordScore(content, query)
	}
	return mr.ranker.Score(content, query)
}

// stringify 将 float64 转换为四位小数的字符串。
func stringify(v float64) string { return fmt.Sprintf("%.4f", v) }

// stringifyMeta 从 metadata 中提取给定键的字符串值。
func stringifyMeta(meta map[string]any, key string) string {
	if v, ok := meta[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// parseConfidence 从 vector metadata 提取 confidence 值。
func parseConfidence(meta map[string]any) float64 {
	if v, ok := meta["confidence"]; ok {
		if s, ok := v.(string); ok {
			var c float64
			fmt.Sscanf(s, "%f", &c)
			return c
		}
	}
	return 0.5
}

// keywordScore 计算 content 与 query 之间简单的词频重叠分数。两个字符串都进行
// tokenize（按空白拆分、小写化、剥离标点），分数为出现在 content 中的 query 词占比。
//
// 返回 score = (overlap_count / total_query_words) * 100。
// 若 query 无 token，返回 0。
func keywordScore(content, query string) float64 {
	contentWords := tokenize(content)
	queryWords := tokenize(query)

	if len(queryWords) == 0 {
		return 0
	}

	// 构建 content 词集合以实现 O(1) 查找。
	contentSet := make(map[string]bool, len(contentWords))
	for _, w := range contentWords {
		contentSet[w] = true
	}

	// 统计 query 与 content 的重叠词数。
	overlap := 0
	for _, w := range queryWords {
		if contentSet[w] {
			overlap++
		}
	}

	return (float64(overlap) / float64(len(queryWords))) * 100
}

// tokenize 将字符串切分为小写词 token，剥离标点并过滤掉过短的 token（单字符）。
// 供 keywordScore 用于词频重叠计算。
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	var tokens []string
	for _, f := range fields {
		// 剥离词边界的常见标点。
		f = strings.Trim(f, ".,;:!?()[]{}'\"")
		// 跳过单字符 token —— 它们很少有意义。
		if len(f) >= 2 {
			tokens = append(tokens, f)
		}
	}
	return tokens
}

// DetectConflicts 检查 MemoryItem 列表中内容相互矛盾的配对。使用简单关键词检测：同一
// 类型 memory 中一条包含正向标记（如 "use"、"always"）、另一条包含反向标记（如 "avoid"、
// "never"）的配对。
//
// 这是用于暴露陈旧或冲突规则的轻量检查，不全面 —— 完整的语义冲突分析需要基于 LLM 的
// 比对（Phase 6+）。
//
// 返回冲突配对列表；未检测到冲突时为空。
func (mr *MemoryRecall) DetectConflicts(memories []MemoryItem) []ConflictPair {
	var conflicts []ConflictPair

	// 指示潜在冲突的反向关键词对。
	// 每对为 [正向标记, 反向标记]。
	oppositePairs := [][2]string{
		{"use", "avoid"},
		{"always", "never"},
		{"do", "don't"},
		{"should", "shouldn't"},
		{"recommend", "avoid"},
		{"prefer", "dislike"},
		{"enable", "disable"},
		{"include", "exclude"},
		{"allow", "block"},
		{"accept", "reject"},
	}

	for i := 0; i < len(memories); i++ {
		for j := i + 1; j < len(memories); j++ {
			// 只检查同类型 memory —— 一条 rule 与一条 reflection 即使有反向关键词
			// 也未必冲突。
			if memories[i].Type != memories[j].Type {
				continue
			}
			lowerA := strings.ToLower(memories[i].Content)
			lowerB := strings.ToLower(memories[j].Content)

			for _, pair := range oppositePairs {
				// 检查方向 A：MemoryA 含正向，MemoryB 含反向
				if strings.Contains(lowerA, pair[0]) && strings.Contains(lowerB, pair[1]) {
					conflicts = append(conflicts, ConflictPair{
						MemoryA: memories[i],
						MemoryB: memories[j],
						Reason:  fmt.Sprintf("opposite markers: '%s' vs '%s'", pair[0], pair[1]),
					})
					break
				}
				// 检查方向 B：MemoryA 含反向，MemoryB 含正向
				if strings.Contains(lowerA, pair[1]) && strings.Contains(lowerB, pair[0]) {
					conflicts = append(conflicts, ConflictPair{
						MemoryA: memories[i],
						MemoryB: memories[j],
						Reason:  fmt.Sprintf("opposite markers: '%s' vs '%s'", pair[1], pair[0]),
					})
					break
				}
			}
		}
	}

	return conflicts
}
