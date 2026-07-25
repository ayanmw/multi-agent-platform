package tool

import (
	"fmt"
	"strings"
	"unicode"
)

// ParseShellCommand 把一条命令模板字符串拆分为 "program" 和参数切片。
// 它采用最小化的类 shell 解析规则：按空白分割，支持双引号包裹以保留
// 内部空格，支持单引号包裹；反斜杠转义下一个字符。未被引号包裹的部分
// 按空白切分。
//
// 模板中允许保留 {param} 占位符：占位符本身作为一个整体 token 被识别，
// 不会在这段解析期间被替换（替换由调用方在生成最终 args 时再做）。这样
// 可防止占位符内部的空格或 metacharacter 被 shell 解释器错误拆分。
//
// 返回的 program 是拆分后的第一个元素，后续 token 为 args。若命令字符串
// 仅含空白或为空，返回错误。
func ParseShellCommand(cmd string) (program string, args []string, err error) {
	tokens, err := splitShellCommand(cmd)
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		return "", nil, fmt.Errorf("empty command")
	}
	return tokens[0], tokens[1:], nil
}

// splitShellCommand 把命令字符串拆分为未替换的 token 列表。
func splitShellCommand(cmd string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	inDouble := false
	inSingle := false
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for _, r := range cmd {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if inSingle {
			if r == '\'' {
				inSingle = false
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if inDouble {
			if r == '"' {
				inDouble = false
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '"':
			inDouble = true
		case '\'':
			inSingle = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, fmt.Errorf("command ends with unmatched backslash escape")
	}
	if inDouble || inSingle {
		return nil, fmt.Errorf("command has unmatched quote")
	}
	flush()
	return tokens, nil
}

// HasShellMetacharacters 检查命令字符串是否含有需要 shell 解释器的元字符。
// 这些元字符包括管道、重定向、逻辑与/或、命令替换、后台执行、注释等。
// 如果存在，说明该命令无法安全地通过 program+args 直接执行，需要 shell
// 解释器（未来可通过显式 shell:true 标记启用）。
// {param} 占位符本身不会被视作 metacharacter，因为它们是调用前的模板标记。
func HasShellMetacharacters(cmd string) bool {
	inPlaceholder := false
	for i, r := range cmd {
		if r == '{' {
			inPlaceholder = true
			continue
		}
		if r == '}' && inPlaceholder {
			inPlaceholder = false
			continue
		}
		if inPlaceholder {
			continue
		}
		switch r {
		case '|', '&', ';', '<', '>', '$', '`', '#', '(', ')', '*', '?', '[', ']':
			return true
		case '~':
			// 只有在 token 开头或紧跟 / 的 ~ 才是 shell 扩展（如 ~/file）。
			// 路径中间如 path~/file 不算扩展。
			if i == 0 {
				return true
			}
			prev := []rune(cmd)[i-1]
			if prev == '=' || prev == ':' || unicode.IsSpace(prev) {
				return true
			}
		}
	}
	return false
}

// ReplaceCommandPlaceholders 把 tokens 中的 {param} 占位符替换为 input map
// 中的对应值。未找到的占位符保持原样。替换后的 token 不会再被 shell 拆分。
func ReplaceCommandPlaceholders(tokens []string, input map[string]any) []string {
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		out[i] = replacePlaceholders(tok, input)
	}
	return out
}

// replacePlaceholders 替换单个 token 中的所有 {key} 占位符。
func replacePlaceholders(tok string, input map[string]any) string {
	result := tok
	for key, value := range input {
		placeholder := fmt.Sprintf("{%s}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}
	return result
}
