package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultCommandScanDir 是 .claude/commands 扫描目录模板。
const DefaultCommandScanDir = ".claude/commands"

// CommandLoader 负责从文件系统扫描 SkillCommand 并注册到 CommandRegistry。
type CommandLoader struct {
	registry *CommandRegistry
	bus      EventBus
}

// NewCommandLoader 创建 CommandLoader。
func NewCommandLoader(registry *CommandRegistry, bus EventBus) *CommandLoader {
	return &CommandLoader{registry: registry, bus: bus}
}

// Registry 返回内部持有的 CommandRegistry。
func (cl *CommandLoader) Registry() *CommandRegistry {
	return cl.registry
}

// LoadGlobal 扫描 baseDir 下的全局 commands。
func (cl *CommandLoader) LoadGlobal(baseDir string) error {
	return cl.LoadForWorkdir(baseDir, "")
}

// LoadForWorkdir 扫描 workdir 下的 .claude/commands/**/*.md。
// projectID 可为空；workdir 必须非空。
func (cl *CommandLoader) LoadForWorkdir(workdir, projectID string) error {
	if workdir == "" {
		return nil
	}
	root := filepath.Join(workdir, DefaultCommandScanDir)
	scope := SkillCommandScopeProject
	if projectID == "" {
		scope = SkillCommandScopeGlobal
	}

	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		cmd, err := parseCommandFile(path, rel, scope, workdir, projectID)
		if err != nil {
			return err
		}
		// M13：frontmatter id 字符校验。非法 id（如 `../evil`、含空格）回退到 rel 路径生成的 id，
		// 避免路径遍历或 skill ID 碰撞。parseCommandFile 已按 command>id>rel 顺序生成，
		// 这里再做一次校验并在非法时用 rel 兜底。
		if !isValidCommandID(cmd.ID) {
			fallback := filepath.ToSlash(strings.TrimSuffix(rel, ".md"))
			fallback = strings.ReplaceAll(fallback, "/", ":")
			if isValidCommandID(fallback) {
				cmd.ID = fallback
				if cmd.Name == "" {
					cmd.Name = fallback
				}
			} else {
				// 跳过无法生成合法 id 的文件，避免污染 registry。
				cl.broadcast(EventSkillCommandChanged, "", map[string]any{
					"reason": "invalid command id",
					"path":   path,
				})
				return nil
			}
		}
		cl.registry.Register(*cmd)
		cl.broadcast(EventSkillCommandLoaded, cmd.ID, map[string]any{
			"id":            cmd.ID,
			"scope":         string(cmd.Scope),
			"workspace_dir": cmd.WorkspaceDir,
		})
		return nil
	})
}

// UnloadForWorkdir 卸载该 workdir 下的所有 project scope commands（review M12）。
// 旧实现复用 ListForWorkdir（同时返回 global + 匹配 project），会把全局命令一并删除且不再重载。
// 这里只卸载 Scope==project && WorkspaceDir==workdir 的条目，跳过 global。
func (cl *CommandLoader) UnloadForWorkdir(workdir string) {
	if workdir == "" {
		return
	}
	for _, cmd := range cl.registry.List("") {
		if cmd.Scope != SkillCommandScopeProject {
			continue
		}
		if cmd.WorkspaceDir != workdir {
			continue
		}
		cl.registry.UnregisterScoped(cmd)
		cl.broadcast(EventSkillCommandUnloaded, cmd.ID, map[string]any{
			"id":            cmd.ID,
			"scope":         string(cmd.Scope),
			"workspace_dir": cmd.WorkspaceDir,
		})
	}
}

// RefreshAll 全量刷新 commands：卸载所有，再重扫全局 + workdirs。
// 返回实际 loaded / unloaded 数量。
func (cl *CommandLoader) RefreshAll(globalBaseDir string, workdirs []string, projectIDs map[string]string) (int, int, error) {
	unloaded := len(cl.registry.List(""))
	cl.registry.Clear()
	if err := cl.LoadGlobal(globalBaseDir); err != nil {
		return 0, unloaded, err
	}
	for _, wd := range workdirs {
		pid := projectIDs[wd]
		if err := cl.LoadForWorkdir(wd, pid); err != nil {
			return 0, unloaded, err
		}
	}
	loaded := len(cl.registry.List(""))
	cl.broadcast(EventSkillCommandChanged, "", map[string]any{
		"reason": "refresh_all",
	})
	return loaded, unloaded, nil
}

// commandFileFrontmatter 表示 command 文件 YAML frontmatter 的可识别字段。
type commandFileFrontmatter struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Skill       string   `yaml:"skill"`
	Command     string   `yaml:"command"`
	Tags        []string `yaml:"tags"`
	Icon        string   `yaml:"icon"`
}

// commandFrontmatterRegex 匹配 command 文件的 YAML frontmatter。
// 第二个 `---` 前换行设为可选（\n?）以兼容空 frontmatter；CRLF 已在 parseCommandFile 入口归一化为 LF。
var commandFrontmatterRegex = regexp.MustCompile(`(?s)^---\n(.*?)\n?---\n(.*)$`)

// parseCommandFile 解析单个 command markdown 文件。
func parseCommandFile(path, rel string, scope SkillCommandScope, workdir, projectID string) (*SkillCommand, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	// 归一化 CRLF → LF（与 file_loader 对齐），避免 Windows 编辑器 \r\n 使正则失配。
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	var body string
	fm := commandFileFrontmatter{}

	matches := commandFrontmatterRegex.FindStringSubmatch(content)
	if len(matches) == 3 {
		if err := yaml.Unmarshal([]byte(matches[1]), &fm); err != nil {
			return nil, fmt.Errorf("invalid frontmatter: %w", err)
		}
		body = strings.TrimSpace(matches[2])
	} else {
		body = strings.TrimSpace(content)
	}

	// ID generation: command > id > rel path with ':' replacing path separators.
	id := fm.Command
	if id == "" {
		id = fm.ID
	}
	if id == "" {
		id = filepath.ToSlash(strings.TrimSuffix(rel, ".md"))
		id = strings.ReplaceAll(id, "/", ":")
	}
	if id == "" {
		return nil, fmt.Errorf("command id cannot be empty")
	}

	name := fm.Name
	if name == "" {
		name = id
	}

	return &SkillCommand{
		ID:           id,
		Name:         name,
		Description:  fm.Description,
		SourcePath:   path,
		Scope:        scope,
		WorkspaceDir: workdir,
		ProjectID:    projectID,
		SkillID:      strings.TrimSpace(fm.Skill),
		Prompt:       body,
		Tags:         fm.Tags,
		Icon:         fm.Icon,
		CommandKey:   strings.TrimSpace(fm.Command),
	}, nil
}

// broadcast 向事件总线发送 command 事件；bus 为 nil 时静默跳过。
func (cl *CommandLoader) broadcast(eventType, id string, data map[string]any) {
	if cl.bus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["event_type"] = eventType
	data["id"] = id
	cl.bus.SendEvent(data)
}
