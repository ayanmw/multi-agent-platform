package config

// 本文件是 internal/config/dotenv 包向 internal/config 的薄封装，
// 用于保持向后兼容：现有调用 config.Getenv / config.LookupEnv 的代码继续工作。
// 新代码建议直接 import "github.com/ayanmw/multi-agent-platform/internal/config/dotenv"。

import (
	"github.com/ayanmw/multi-agent-platform/internal/config/dotenv"
)

// LookupEnvResult 是 dotenv.LookupEnvResult 的别名，保持现有类型引用有效。
type LookupEnvResult = dotenv.LookupEnvResult

// ReloadEnvCache 从由 EnvFilePath() 解析出的 .env 文件重新加载默认实例缓存。
func ReloadEnvCache() error {
	return dotenv.Reload()
}

// EnvFilePath 返回应加载的 .env 文件路径。
func EnvFilePath() string {
	return dotenv.FilePath()
}

// LoadEnvFile 将指定路径的 .env 文件加载到默认实例缓存。
func LoadEnvFile(path string) error {
	return dotenv.LoadFile(path)
}

// ApplyEnvFileToOS 读取 .env 文件并将其中声明的变量写入系统环境变量（仅当不存在时）。
func ApplyEnvFileToOS(path string) error {
	return dotenv.ApplyEnvFileToOS(path)
}

// SetDotEnvFirst 让默认实例 Getenv 默认返回 .env 中的值。
func SetDotEnvFirst() {
	dotenv.SetDotEnvFirst()
}

// SetOSFirst 让默认实例 Getenv 默认返回系统环境变量中的值。
func SetOSFirst() {
	dotenv.SetOSFirst()
}

// ResetEnvCache 清空默认实例 .env 缓存并恢复 .env 优先策略。
func ResetEnvCache() {
	dotenv.ResetCache()
}

// Getenv 按默认实例当前优先级策略返回环境变量值。
func Getenv(key string) string {
	return dotenv.Getenv(key)
}

// LookupEnv 类似 Getenv，但额外返回两个来源的存在性。
func LookupEnv(key string) LookupEnvResult {
	return dotenv.LookupEnv(key)
}
