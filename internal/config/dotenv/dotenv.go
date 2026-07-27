// Package dotenv 提供与 .env 文件相关的环境变量加载、缓存与优先级读取能力。
//
// 该包 intentionally 独立于 internal/config，允许其他 internal 包（如 internal/tool）
// 复用 .env 读取能力而不会引入 config 包及其中间模块，避免循环依赖。
//
// 设计：
//   - Dotenv 是可实例化的状态对象；New/NewWithPath 创建的实例拥有独立的缓存与优先级。
//   - 包级函数（Getenv/LoadFile 等）操作一个进程级默认实例 defaultDotenv。
//   - 默认实例在包 init 时自动加载默认 .env（受 ENV_FILE 影响），满足“启动即读取”需求。
//   - 单元测试应创建独立 Dotenv 实例，避免污染全局缓存或被全局缓存污染。
package dotenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
)

// Priority 控制 Getenv 的查找顺序。
type Priority int

const (
	// DotEnvFirst 表示 .env 文件中的变量优先于系统环境变量。
	DotEnvFirst Priority = iota
	// OSFirst 表示系统环境变量优先于 .env 文件中的变量。
	OSFirst
)

// Dotenv 持有 .env 缓存与优先级状态。实例之间完全独立。
type Dotenv struct {
	mu       sync.RWMutex
	cache    map[string]string
	priority Priority
	path     string
}

// New 创建一个空的 Dotenv 实例，不加载任何 .env 文件，优先级为 DotEnvFirst。
// 适合需要独立 env 环境的测试或命令行工具。
func New() *Dotenv {
	return &Dotenv{
		cache:    map[string]string{},
		priority: DotEnvFirst,
	}
}

// NewWithPath 创建一个 Dotenv 实例并立即加载指定路径的 .env 文件。
// 路径为空时使用默认路径（ENV_FILE 或 CWD/.env）。仅加载错误会返回 error。
func NewWithPath(path string) (*Dotenv, error) {
	d := New()
	if path == "" {
		path = FilePath()
	}
	if err := d.LoadFile(path); err != nil {
		return nil, err
	}
	return d, nil
}

// Default 返回进程级默认 Dotenv 实例。包级函数都转发给它。
func Default() *Dotenv {
	return defaultDotenv
}

var defaultDotenv *Dotenv

func init() {
	defaultDotenv = New()
	// 启动时默认加载 .env（ENV_FILE 可重定位）。失败不致命：缓存保持空，Getenv 回退 os.Getenv。
	_ = defaultDotenv.Reload()
}

// FilePath 返回应加载的 .env 文件路径。
// 优先取 ENV_FILE 系统环境变量，否则回退到当前工作目录下的 ".env"。
// 该函数本身使用 os.Getenv，因为它决定 .env 文件位置，不能被缓存影响。
func FilePath() string {
	if v := os.Getenv("ENV_FILE"); v != "" {
		return v
	}
	return ".env"
}

// SetPath 设置本实例后续 Reload 时使用的默认路径。
func (d *Dotenv) SetPath(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.path = path
}

// LoadFile 将指定路径的 .env 文件加载到本实例内存缓存，不写入系统环境变量。
// 解析使用 github.com/joho/godotenv，支持引号、注释、空行、export 前缀。
// 文件不存在时清空缓存并返回 nil。
func (d *Dotenv) LoadFile(path string) error {
	m := make(map[string]string)
	if path != "" {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			d.setCache(m)
			return nil
		}
		return err
	}

	loaded, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	for k, v := range loaded {
		m[k] = v
	}
	d.setCache(m)

	// .env 加载完毕后，检查同一文件中是否声明了优先级开关。
	// ENV_FILE_PRIORITY=os 表示系统环境变量优先；其他值保持 .env 优先。
	if p := m["ENV_FILE_PRIORITY"]; p == "os" {
		d.SetOSFirst()
	} else {
		d.SetDotEnvFirst()
	}
	return nil
}

// Reload 重新加载本实例配置的路径。若未通过 SetPath 指定，则使用 FilePath()。
func (d *Dotenv) Reload() error {
	path := d.path
	if path == "" {
		path = FilePath()
	}
	return d.LoadFile(path)
}

// ApplyEnvFileToOS 读取 .env 文件并将其中声明的变量写入系统环境变量，但仅当
// 系统环境变量尚未存在时才写入。这是原 loadEnvFile 行为的公开版本，供单元测试
// 或需要保持 os.Getenv 也能读取 .env 的场景使用。
func (d *Dotenv) ApplyEnvFileToOS(path string) error {
	m, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	for key, val := range m {
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetDotEnvFirst 让本实例默认返回 .env 中的值，只有当 .env 未定义某 key 时
// 才回退到 os.Getenv。这是 server 启动时的默认行为。
func (d *Dotenv) SetDotEnvFirst() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.priority = DotEnvFirst
}

// SetOSFirst 让本实例默认返回系统环境变量中的值，仅当系统环境变量未定义时
// 才回退到 .env。需要在程序启动早期调用。
func (d *Dotenv) SetOSFirst() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.priority = OSFirst
}

// ResetCache 清空本实例 .env 缓存并恢复 .env 优先策略。主要用于单元测试隔离。
func (d *Dotenv) ResetCache() {
	d.setCache(map[string]string{})
	d.SetDotEnvFirst()
}

func (d *Dotenv) setCache(m map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cache = m
}

// cacheValue 返回本实例缓存中 key 的值以及是否存在于缓存。
func (d *Dotenv) cacheValue(key string) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cache == nil {
		return "", false
	}
	v, ok := d.cache[key]
	return v, ok
}

func (d *Dotenv) currentPriority() Priority {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.priority
}

// Getenv 按本实例当前优先级策略返回环境变量值。
//   - 默认（SetDotEnvFirst）：先查 .env 缓存，未定义时回退 os.Getenv。
//   - SetOSFirst：先查 os.Getenv，未定义时回退 .env 缓存。
func (d *Dotenv) Getenv(key string) string {
	return d.LookupEnv(key).Value
}

// LookupEnvResult 是 LookupEnv 的返回值，包含最终值与两个来源的存在性。
type LookupEnvResult struct {
	Value    string
	InDotEnv bool
	InOS     bool
}

// LookupEnv 类似 Getenv，但额外返回值是否来自 .env 缓存、系统环境变量，或两者都存在。
func (d *Dotenv) LookupEnv(key string) LookupEnvResult {
	dotVal, inDot := d.cacheValue(key)
	osVal, inOS := os.LookupEnv(key)

	if d.currentPriority() == OSFirst {
		if inOS {
			return LookupEnvResult{Value: osVal, InDotEnv: inDot, InOS: true}
		}
		return LookupEnvResult{Value: dotVal, InDotEnv: inDot, InOS: false}
	}

	if inDot {
		return LookupEnvResult{Value: dotVal, InDotEnv: true, InOS: inOS}
	}
	return LookupEnvResult{Value: osVal, InDotEnv: false, InOS: inOS}
}

// GetenvWithDefault 返回 key 的环境变量值，若未设置则返回 defaultVal。
func (d *Dotenv) GetenvWithDefault(key, defaultVal string) string {
	if v := d.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// MustBool 读取 key 并解析为 bool。接受 "true"/"1"/"yes"/"on" 为真，
// "false"/"0"/"no"/"off" 为假。空值或无法识别时返回 defaultVal。
func (d *Dotenv) MustBool(key string, defaultVal bool) bool {
	v := strings.ToLower(strings.TrimSpace(d.Getenv(key)))
	if v == "" {
		return defaultVal
	}
	switch v {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return defaultVal
}

// MustInt 读取 key 并解析为 int。空值或解析失败时返回 defaultVal。
func (d *Dotenv) MustInt(key string, defaultVal int) int {
	v := strings.TrimSpace(d.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// Must 在环境变量未设置或为空时返回提供的默认值，否则返回该值本身。
// 与 GetenvWithDefault 相同，用于与 MustBool/MustInt 风格一致的场景。
func (d *Dotenv) Must(key string, defaultVal string) string {
	return d.GetenvWithDefault(key, defaultVal)
}

// Expand 将 s 中 ${key} 或 $key 形式的环境变量引用展开为当前 dotenv 优先级下的值。
// 未定义的引用保留为空字符串。
func (d *Dotenv) Expand(s string) string {
	return os.Expand(s, d.Getenv)
}

// Dump 返回当前 .env 缓存的副本，仅用于测试与调试。返回 map 是浅拷贝。
func (d *Dotenv) Dump() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cache == nil {
		return map[string]string{}
	}
	m := make(map[string]string, len(d.cache))
	for k, v := range d.cache {
		m[k] = v
	}
	return m
}

// Set 在内存缓存中设置一个值，不写入 .env 文件也不写入系统环境变量。
// 仅用于测试强制注入场景。
func (d *Dotenv) Set(key, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cache == nil {
		d.cache = map[string]string{}
	}
	d.cache[key] = value
}

// 包级函数：操作默认实例 defaultDotenv，保持旧代码兼容。

// LoadFile 将指定路径的 .env 文件加载到默认实例。
func LoadFile(path string) error { return Default().LoadFile(path) }

// Reload 重新加载默认实例的 .env。
func Reload() error { return Default().Reload() }

// ApplyEnvFileToOS 读取 .env 并写入系统环境变量（仅当不存在时）。
func ApplyEnvFileToOS(path string) error { return Default().ApplyEnvFileToOS(path) }

// SetDotEnvFirst 让默认实例 .env 优先。
func SetDotEnvFirst() { Default().SetDotEnvFirst() }

// SetOSFirst 让默认实例系统环境变量优先。
func SetOSFirst() { Default().SetOSFirst() }

// ResetCache 清空默认实例缓存并恢复 .env 优先。
func ResetCache() { Default().ResetCache() }

// Getenv 按默认实例优先级返回环境变量值。
func Getenv(key string) string { return Default().Getenv(key) }

// LookupEnv 按默认实例优先级返回环境变量值与来源。
func LookupEnv(key string) LookupEnvResult { return Default().LookupEnv(key) }

// GetenvWithDefault 返回环境变量值或默认值。
func GetenvWithDefault(key, defaultVal string) string { return Default().GetenvWithDefault(key, defaultVal) }

// MustBool 返回 bool 环境变量值。
func MustBool(key string, defaultVal bool) bool { return Default().MustBool(key, defaultVal) }

// MustInt 返回 int 环境变量值。
func MustInt(key string, defaultVal int) int { return Default().MustInt(key, defaultVal) }

// Must 返回环境变量值或默认值。
func Must(key string, defaultVal string) string { return Default().Must(key, defaultVal) }

// Expand 展开字符串中的环境变量引用。
func Expand(s string) string { return Default().Expand(s) }

// Dump 返回默认实例缓存副本。
func Dump() map[string]string { return Default().Dump() }

// Set 在默认实例缓存中设置值。
func Set(key, value string) { Default().Set(key, value) }

// String 仅用于调试，不适合正式 API。
func (p Priority) String() string {
	switch p {
	case DotEnvFirst:
		return "dotenv-first"
	case OSFirst:
		return "os-first"
	default:
		return fmt.Sprintf("priority(%d)", p)
	}
}
