// template.go — 定时器触发时的模板渲染。
//
// 所有 action_payload 的字符串字段（以及 start_task 的 input）都支持占位符，
// 让定时器能基于"上次执行结果"动态生成新一轮输入。注入的变量：
//
//   .Now          触发时刻（RFC3339）
//   .PrevTrigger  上次触发时刻（RFC3339，首次为空）
//   .PrevStatus   上次执行状态（completed/failed/skipped/missed，首次为空）
//   .PrevResult   上次 final_result 摘要（首次为空）
//   .Count        第几次触发（从 1 开始）
//   .CronID       当前 cron ID
//   .CronName     当前 cron 名称
//
// 设计取舍：使用标准库 text/template（而非自定义 {{variable}} 渲染器），
// 因为 cron 的模板需求天然带"上一次结果"这种条件化场景，text/template
// 的条件/管道能力比 Skill 的简单占位符替换更合适。
// 渲染失败时保留原始模板字符串（不返回错误），避免一次模板笔误阻断整个触发。
package cron

import (
	"bytes"
	"strings"
	"text/template"
)

// TemplateContext 是渲染模板时注入的变量集合。
type TemplateContext struct {
	Now         string // RFC3339
	PrevTrigger string // RFC3339，首次为空
	PrevStatus  string
	PrevResult  string
	Count       int
	CronID      string
	CronName    string
}

// RenderTemplate 渲染单个字符串模板。渲染失败返回原始 tmpl。
func RenderTemplate(tmpl string, ctx TemplateContext) string {
	if tmpl == "" || !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	t, err := template.New("cron").Parse(tmpl)
	if err != nil {
		return tmpl
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return tmpl
	}
	return buf.String()
}

// RenderMap 递归渲染 map 中所有字符串值（含嵌套 map 与 []any 中的字符串），
// 返回新的 map。非字符串值原样保留。用于渲染 action_payload。
func RenderMap(payload map[string]any, ctx TemplateContext) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = renderValue(v, ctx)
	}
	return out
}

// renderValue 对任意值递归渲染其中的字符串。
func renderValue(v any, ctx TemplateContext) any {
	switch val := v.(type) {
	case string:
		return RenderTemplate(val, ctx)
	case map[string]any:
		return RenderMap(val, ctx)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = renderValue(item, ctx)
		}
		return out
	default:
		return v
	}
}
