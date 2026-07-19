// Package version 提供应用程序版本字符串，从本目录下的 version.txt 读取。
// 这是所有模块（Go 后端、Vue 前端、HTML 文档）版本信息的唯一来源。
//
// 用法：
//
//	import "github.com/anmingwei/multi-agent-platform/internal/version"
//	fmt.Println(version.Version) // "v0.4 Alpha"
//
// 版本字符串通过 go:embed 在编译时嵌入。
// 前端在运行时从 /api/version endpoint 读取该版本。
// HTML 文档应同步更新以匹配此版本（参见 update-doc-versions.sh）。
package version

import (
	_ "embed"
	"strings"
)

// rawVersion 是从 version.txt 嵌入的原始版本字符串，编译时通过 go:embed 注入。
//
//go:embed version.txt
var rawVersion string

// Version 是去除首尾空白后的版本字符串（不含末尾换行）。
var Version = strings.TrimSpace(rawVersion)