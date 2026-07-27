package config

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// envCache 保存已解析的 .env 键值对，避免每次 Getenv 都重新读文件。
// Load() 会调用 ReloadEnvCache 预热缓存。
// envPriorityMode 控制 Getenv 的查找顺序。
type envPriorityMode int

const (
	// envPriorityDotEnv 表示 .env 中的变量优先于系统环境变量。
	envPriorityDotEnv envPriorityMode = iota
	// envPriorityOS 表示系统环境变量优先于 .env 中的变量。
	envPriorityOS
)

var (
	envCache    map[string]string
	envCacheMu  sync.RWMutex
	envPriority envPriorityMode // 默认 .env 优先
)

// ReloadEnvCache 从由 EnvFilePath() 解析出的 .env 文件重新加载缓存。
// 若文件不存在或不可读，缓存会被清空（后续 Getenv 直接回退到 os.Getenv）。
func ReloadEnvCache() error {
	return LoadEnvFile(EnvFilePath())
}

// EnvFilePath 返回应加载的 .env 文件路径：优先取 ENV_FILE 系统环境变量，
// 否则回退到当前工作目录下的 ".env"。
// 该函数本身使用 os.Getenv，因为它决定了 .env 文件的位置，不能被缓存影响。
func EnvFilePath() string {
	if v := os.Getenv("ENV_FILE"); v != "" {
		return v
	}
	return ".env"
}

// LoadEnvFile 将指定路径的 .env 文件加载到内存缓存。它不会写入系统环境变量，
// 因此与 os.Getenv 保持独立。供单元测试需要精确控制 .env 缓存时使用。
func LoadEnvFile(path string) error {
	m := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			setEnvCache(m)
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		m[key] = val
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	setEnvCache(m)

	// .env 加载完毕后，检查同一文件中是否声明了优先级开关。
	// 注意：ENV_FILE_PRIORITY 取值 "os" 表示系统环境变量优先，其他（含空）保持 .env 优先。
	if p := m["ENV_FILE_PRIORITY"]; p == "os" {
		SetOSFirst()
	} else {
		SetDotEnvFirst()
	}
	return nil
}

// ApplyEnvFileToOS 读取 .env 文件并将其中声明的变量写入系统环境变量，但仅当
// 系统环境变量尚未存在时才写入。这是原 loadEnvFile 行为的公开版本，供单元测试
// 或需要保持 os.Getenv 也能读取 .env 的场景使用。
func ApplyEnvFileToOS(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, val); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// SetDotEnvFirst 让 Getenv 默认返回 .env 中的值，只有当 .env 未定义某 key 时
// 才回退到 os.Getenv。这是 server 启动时的默认行为，便于项目级配置作为真相源。
func SetDotEnvFirst() {
	envCacheMu.Lock()
	defer envCacheMu.Unlock()
	envPriority = envPriorityDotEnv
}

// SetOSFirst 让 Getenv 默认返回系统环境变量中的值，仅当系统环境变量未定义时
// 才回退到 .env。需要在程序启动早期调用，否则 .env 缓存中的值可能已经生效。
func SetOSFirst() {
	envCacheMu.Lock()
	defer envCacheMu.Unlock()
	envPriority = envPriorityOS
}

// ResetEnvCache 清空 .env 缓存并恢复 .env 优先策略。主要用于单元测试隔离。
func ResetEnvCache() {
	setEnvCache(map[string]string{})
	SetDotEnvFirst()
}

func setEnvCache(m map[string]string) {
	envCacheMu.Lock()
	defer envCacheMu.Unlock()
	envCache = m
}

// envCacheValue 返回缓存中 key 的值以及是否存在于缓存。
func envCacheValue(key string) (string, bool) {
	envCacheMu.RLock()
	defer envCacheMu.RUnlock()
	if envCache == nil {
		return "", false
	}
	v, ok := envCache[key]
	return v, ok
}

// currentEnvPriority 返回当前优先级（加锁读取）。
func currentEnvPriority() envPriorityMode {
	envCacheMu.RLock()
	defer envCacheMu.RUnlock()
	return envPriority
}

// Getenv 按当前优先级策略返回环境变量值。
//   - 默认（SetDotEnvFirst）：先查 .env 缓存，未定义时回退 os.Getenv。
//   - SetOSFirst：先查 os.Getenv，未定义时回退 .env 缓存。
//
// 该函数封装了所有需要 .env 优先逻辑的环境变量读取点。
func Getenv(key string) string {
	return LookupEnv(key).Value
}

// LookupEnvResult 是 LookupEnv 的返回值，包含最终值与两个来源的存在性。
type LookupEnvResult struct {
	Value        string
	InDotEnv     bool
	InOS         bool
}

// LookupEnv 类似 Getenv，但额外返回值是否来自 .env 缓存、系统环境变量，或两者都存在。
func LookupEnv(key string) LookupEnvResult {
	dotVal, inDot := envCacheValue(key)
	osVal, inOS := os.LookupEnv(key)

	if currentEnvPriority() == envPriorityOS {
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
