// Package llm —— RateLimiter，基于模型 RPM（每分钟请求数）的滑动窗口限流器。
//
// # 设计理由
//
// 多 model 路由场景下，不同模型有不同 rate limit。Router 需要在选择候选模型
// 时排除当前已经触发限流的模型，避免任务因 429 反复失败。
//
// RateLimiter 使用每分钟滑动窗口：对每个模型保留最近 60 秒内的调用时间戳，
// 当窗口内调用次数 >= RateLimitRPM 时视为超限。RPM=0 表示无限制（本地模型）。
//
// # 用法
//
//	lim := llm.NewRateLimiter()
//	lim.RecordCall("deepseek-v4-flash")
//	if lim.IsLimitExceeded("deepseek-v4-flash") { ... }
//
// # 线程安全
//
// RateLimiter 使用 sync.Mutex 保护内部状态，可安全并发使用。
package llm

import (
	"sync"
	"time"
)

// RateLimiter 基于滑动窗口追踪每个模型的调用速率。
// 当模型未在 DefaultProfiles() 中找到时，fallbackLimit 作为默认 RPM 使用；
// 0 表示无限制。
type RateLimiter struct {
	mu            sync.Mutex
	timestamps    map[string][]time.Time // model → 最近调用时间戳
	limits        map[string]int         // model → 覆盖的 RPM 限制
	window        time.Duration
	fallbackLimit int
}

// NewRateLimiter 创建一个新的模型级 RPM 限流器。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		timestamps:    make(map[string][]time.Time),
		limits:        make(map[string]int),
		window:        time.Minute,
		fallbackLimit: 60, // 未注册模型使用保守默认值 60 RPM
	}
}

// SetLimit 覆盖指定 model 的 RPM 限制，便于测试或动态配置。
func (r *RateLimiter) SetLimit(model string, rpm int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limits[model] = rpm
}

// RecordCall 记录一次对 model 的调用，使用当前时间。
func (r *RateLimiter) RecordCall(model string) {
	r.RecordCallAt(model, time.Now())
}

// RecordCallAt 在指定时间记录一次 model 调用，便于测试控制时间。
func (r *RateLimiter) RecordCallAt(model string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.timestamps[model] = append(r.timestamps[model], at)
}

// IsLimitExceeded 检查 model 当前是否超过其 RateLimitRPM。
//
// 逻辑：保留过去 1 分钟内的调用时间戳；若数量 >= limit 则超限。
// RPM=0 表示无限制，始终返回 false。
func (r *RateLimiter) IsLimitExceeded(model string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	profile := modelProfileFromLimiter(model, r)
	limit := profile.RateLimitRPM
	if override, ok := r.limits[model]; ok {
		limit = override
	}
	if limit <= 0 {
		limit = r.fallbackLimit
	}

	now := time.Now()
	cutoff := now.Add(-r.window)
	calls := r.timestamps[model]
	var recent int
	for _, ts := range calls {
		if ts.After(cutoff) {
			recent++
		}
	}

	return recent > limit
}

// ForgetOldCalls 清理所有模型在 cutoff 时间之前的调用记录。
// 用于周期性 prune 过期时间戳，避免 map 无限增长。
func (r *RateLimiter) ForgetOldCalls(cutoff time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for model, calls := range r.timestamps {
		var kept []time.Time
		for _, ts := range calls {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(r.timestamps, model)
		} else {
			r.timestamps[model] = kept
		}
	}
}

// modelProfileFromLimiter 尝试从全局 DefaultProfiles() 查找模型 RPM；
// 找不到则返回 RateLimitRPM=0（无限制）。
// 注意：RateLimiter 本身不持有 registry，避免与 Router 的 registry 重复。
// 未来可通过选项让 RateLimiter 引用 registry；当前用 profile 名反查足够。
func modelProfileFromLimiter(name string, r *RateLimiter) *ModelProfile {
	for _, p := range DefaultProfiles() {
		if p.Name == name {
			return p
		}
	}
	return &ModelProfile{Name: name, RateLimitRPM: r.fallbackLimit}
}
