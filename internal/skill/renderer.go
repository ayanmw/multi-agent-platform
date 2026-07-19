package skill

import (
	"fmt"
	"regexp"
	"sort"
)

// Renderer 负责渲染 SkillTemplate 的占位符。
// 支持 {{var}} 和 {{ var }} 两种风格。
type Renderer struct {
	re *regexp.Regexp
}

// NewRenderer 创建模板渲染器。
func NewRenderer() *Renderer {
	return &Renderer{
		re: regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`),
	}
}

// Render 渲染单个 SkillTemplate，将占位符替换为 vars 中对应的值。
// 若变量在 vars 中不存在，保留原始占位符。
func (r *Renderer) Render(tmpl SkillTemplate, vars map[string]any) string {
	return r.re.ReplaceAllStringFunc(tmpl.Content, func(match string) string {
		m := r.re.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		name := m[1]
		if v, ok := vars[name]; ok {
			return toString(v)
		}
		return match
	})
}

// ExtractVariables 从模板内容中提取所有占位符变量名，去重并按字母序返回。
func (r *Renderer) ExtractVariables(content string) []string {
	seen := make(map[string]struct{})
	for _, m := range r.re.FindAllStringSubmatch(content, -1) {
		if len(m) >= 2 {
			seen[m[1]] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
