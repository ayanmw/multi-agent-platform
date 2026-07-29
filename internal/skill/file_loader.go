package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultSkillScanDirs 是默认启用的文件系统 Skill 扫描目录模板。
// 每个模板会相对于 baseDir（全局 base 或 session workdir）解析为具体路径。
var DefaultSkillScanDirs = []string{
	".claude/skills",
	".agents/skills",
	".agent/skills",
	".opencode/skills",
}

// SettingStore 是 settings 表的最小抽象，用于读取/写入扫描目录配置。
// pkg/db 会在实处实现；本接口让 internal/skill 不直接 import pkg/db，避免循环依赖。
type SettingStore interface {
	// GetSetting 返回指定 key 的值；不存在时返回空字符串与 nil error。
	GetSetting(key string) (string, error)
	// SetSetting 写入 key/value；value 为空时可选择是否删除该 key。
	SetSetting(key, value string) error
}

// EventBus 是 Skill 扫描过程中用于广播生命周期事件的最小接口。
// 为 nil 时跳过广播，方便单测与无事件总线场景。
type EventBus interface {
	SendEvent(data map[string]any)
}

// globalSkillTaskID 用于无明确 session 的全局事件。
const globalSkillTaskID = "global"

// FileLoader 负责从文件系统扫描 Skill 并注册到内存 Registry。
// 它支持全局扫描（server CWD）与项目级扫描（session workdir），
// 并通过 SettingStore 持久化扫描目录配置。
type FileLoader struct {
	registry *Registry
	store    *Store
	settings SettingStore
	bus      EventBus
}

// NewFileLoader 创建一个 FileLoader。
// registry 用于注册扫描到的 skill；store 支持未来把文件系统 skill 影子写入 DB；
// settings 读取 skill_scan_dirs 配置；bus 用于广播加载/卸载事件（可为 nil）。
func NewFileLoader(registry *Registry, store *Store, settings SettingStore, bus EventBus) *FileLoader {
	return &FileLoader{
		registry: registry,
		store:    store,
		settings: settings,
		bus:      bus,
	}
}

// LoadGlobal 扫描 baseDir 下所有启用目录模板中的 Skill，并注册到 registry。
// baseDir 通常是 server 进程当前工作目录，或环境决定的全局 skill 根目录。
func (fl *FileLoader) LoadGlobal(baseDir string) error {
	dirs, err := getEnabledScanDirs(fl.settings)
	if err != nil {
		return err
	}
	for _, tmpl := range dirs {
		root := filepath.Join(baseDir, tmpl)
		if err := fl.loadDir(root, SkillScopeGlobal, "", ""); err != nil {
			// 单个目录失败不影响全局扫描；记录后继续。
			fl.broadcast(EventSkillLoadFailed, "", map[string]any{
				"path":   root,
				"reason": err.Error(),
			})
		}
	}
	return nil
}

// LoadForWorkdir 扫描指定 workdir 下的 Skill，作为 project scope 注册。
// projectID 可为空；workdir 必须非空。
func (fl *FileLoader) LoadForWorkdir(workdir, projectID string) error {
	if workdir == "" {
		return nil
	}
	dirs, err := getEnabledScanDirs(fl.settings)
	if err != nil {
		return err
	}
	for _, tmpl := range dirs {
		root := filepath.Join(workdir, tmpl)
		if err := fl.loadDir(root, SkillScopeProject, workdir, projectID); err != nil {
			fl.broadcast(EventSkillLoadFailed, "", map[string]any{
				"path":   root,
				"reason": err.Error(),
			})
		}
	}
	return nil
}

// RefreshGlobal 只卸载全局层（source=local_file 且 scope=global）的 skill 并重扫全局，
// 保留所有 project scope local_file skill（review M1）。
// 用于 scan-config 变更：spec 要求"不改变已扫描 workdir"，而 Reload/全量 RefreshAll 会
// 把 project skill 一并清空、之后又不重扫 workdir，导致用户已加载的 project skill 消失。
func (fl *FileLoader) RefreshGlobal(globalBaseDir string) (int, int) {
	if fl.registry == nil {
		return 0, 0
	}
	loaded, unloaded := 0, 0
	// 仅卸载全局层 local_file skill。
	for _, s := range fl.registry.List(nil) {
		if s.Source == SkillSourceLocalFile && s.Scope == SkillScopeGlobal {
			fl.registry.Unregister(s.ID)
			unloaded++
			fl.broadcast(EventSkillUnloaded, s.ID, map[string]any{
				"id":     s.ID,
				"source": string(s.Source),
				"scope":  string(s.Scope),
			})
		}
	}
	// 重扫全局层；统计重扫后全局层 local_file 数量作为 loaded（含原有保留的）。
	before := fl.countGlobalLocalFile()
	if err := fl.LoadGlobal(globalBaseDir); err != nil {
		return loaded, unloaded
	}
	after := fl.countGlobalLocalFile()
	if after > before {
		loaded = after - before
	}
	return loaded, unloaded
}

// countGlobalLocalFile 统计 registry 中 source=local_file 且 scope=global 的 skill 数。
func (fl *FileLoader) countGlobalLocalFile() int {
	n := 0
	for _, s := range fl.registry.List(nil) {
		if s.Source == SkillSourceLocalFile && s.Scope == SkillScopeGlobal {
			n++
		}
	}
	return n
}

// RefreshAll 全量刷新文件系统 Skill。
// 先卸载所有 source=local_file 的 skill，再重扫全局 baseDir + 所有已知 workdirs。
func (fl *FileLoader) RefreshAll(globalBaseDir string, workdirs []string, projectIDs map[string]string) error {
	if fl.registry == nil {
		return nil
	}
	// 1. 卸载所有当前 local_file skill。
	for _, s := range fl.registry.List(nil) {
		if s.Source == SkillSourceLocalFile {
			fl.registry.Unregister(s.ID)
			fl.broadcast(EventSkillUnloaded, s.ID, map[string]any{
				"id":     s.ID,
				"source": string(s.Source),
				"scope":  string(s.Scope),
			})
		}
	}
	// 2. 重新扫描全局。
	if err := fl.LoadGlobal(globalBaseDir); err != nil {
		return err
	}
	// 3. 重新扫描每个 workdir。
	for _, wd := range workdirs {
		pid := projectIDs[wd]
		if err := fl.LoadForWorkdir(wd, pid); err != nil {
			return err
		}
	}
	return nil
}

// loadDir 递归扫描 root 下的直接子目录（每个子目录视为一个 skill-id），
// 读取 SKILL.md 并注册。
func (fl *FileLoader) loadDir(root string, scope SkillScope, workspaceDir, projectID string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(root, e.Name(), "SKILL.md")
		info, err := os.Stat(skillPath)
		if err != nil || info.IsDir() {
			continue
		}
		s, err := parseSkillFile(skillPath, scope, workspaceDir, projectID)
		if err != nil {
			// 解析失败：以 invalid 状态注册，让用户能在列表看到原因。
			invalid := skillFromParseError(skillPath, e.Name(), scope, workspaceDir, projectID, err)
			fl.registerOrUpdate(invalid)
			continue
		}
		fl.registerOrUpdate(*s)
	}
	return nil
}

// registerOrUpdate 把 skill 写进 registry；若已存在同 ID 且来源更“权威”则保留原有。
// 权威顺序：local_db > local_file > built_in。
func (fl *FileLoader) registerOrUpdate(s Skill) {
	if fl.registry == nil {
		return
	}
	existing, ok := fl.registry.Get(s.ID)
	if ok && sourceRank(existing.Source) > sourceRank(s.Source) {
		return
	}
	fl.registry.Register(s)
	fl.broadcast(EventSkillLoaded, s.ID, map[string]any{
		"id":       s.ID,
		"source":   string(s.Source),
		"state":    string(s.State),
		"scope":    string(s.Scope),
		"workdir":  s.WorkspaceDir,
		"path":     s.SourceURL,
		"changed":  ok,
	})
	if ok {
		fl.broadcast(EventSkillChanged, s.ID, map[string]any{
			"id":     s.ID,
			"source": string(s.Source),
			"state":  string(s.State),
		})
	}
}

// sourceRank 返回来源权威等级，数字越大越优先。
func sourceRank(src SkillSource) int {
	switch src {
	case SkillSourceLocalDB:
		return 3
	case SkillSourceLocalFile:
		return 2
	case SkillSourceBuiltIn:
		return 1
	default:
		return 0
	}
}

// skillFileFrontmatter 表示 SKILL.md YAML frontmatter 的可识别字段。
type skillFileFrontmatter struct {
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	DisplayName     string   `yaml:"display_name"`
	Description     string   `yaml:"description"`
	Tags            []string `yaml:"tags"`
	Scope           string   `yaml:"scope"`
	ProjectID       string   `yaml:"project_id"`
	TemplateName    string   `yaml:"template_name"`
	Authors         []string `yaml:"authors"`
	Version         string   `yaml:"version"`
}

// frontmatterRegex 匹配 YAML frontmatter（`---\n...\n---\nbody`）。
// 第二个 `---` 前的换行设为可选（\n?），以兼容空 frontmatter（`---\n---\nbody`）。
// CRLF 已在 parseSkillFile 入口归一化为 LF，故此处只认 \n。
var frontmatterRegex = regexp.MustCompile(`(?s)^---\n(.*?)\n?---\n(.*)$`)

// parseSkillFile 解析单个 SKILL.md 文件为 Skill 对象。
// 目录名作为默认 skill-id；frontmatter 中的 id/name/description/tags/scope 可覆盖默认值。
func parseSkillFile(path string, scope SkillScope, workspaceDir, projectID string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	skillID := filepath.Base(dir)

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	// 归一化 CRLF → LF，避免 Windows 编辑器产生的 \r\n 让 frontmatterRegex（只认 \n）整条不匹配，
	// 进而把含 `---` 分隔符的原文回退为 system_prompt 模板。见 review M2/M3。
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	var body string
	frontmatter := skillFileFrontmatter{}

	matches := frontmatterRegex.FindStringSubmatch(content)
	if len(matches) == 3 {
		if err := yaml.Unmarshal([]byte(matches[1]), &frontmatter); err != nil {
			return nil, fmt.Errorf("invalid frontmatter: %w", err)
		}
		body = strings.TrimSpace(matches[2])
	} else {
		body = strings.TrimSpace(content)
	}

	if frontmatter.ID != "" {
		skillID = frontmatter.ID
	}
	if skillID == "" {
		return nil, fmt.Errorf("skill id cannot be empty")
	}
	if frontmatter.Name != "" {
		frontmatter.DisplayName = frontmatter.Name
	}
	if frontmatter.DisplayName == "" {
		frontmatter.DisplayName = skillID
	}
	if frontmatter.Version == "" {
		frontmatter.Version = "1.0.0"
	}

	// scope 处理：frontmatter 可显式覆盖；否则按默认规则。
	finalScope := scope
	if strings.TrimSpace(frontmatter.Scope) != "" {
		switch SkillScope(strings.TrimSpace(frontmatter.Scope)) {
		case SkillScopeGlobal, SkillScopeProject, SkillScopeSession:
			finalScope = SkillScope(strings.TrimSpace(frontmatter.Scope))
		}
	}
	// Session scope 在本次不注入，但允许文件声明；这里降级为 project 以免运行时遗漏。
	if finalScope == SkillScopeSession {
		finalScope = SkillScopeProject
	}

	templateName := "system_prompt"
	if strings.TrimSpace(frontmatter.TemplateName) != "" {
		templateName = strings.TrimSpace(frontmatter.TemplateName)
	}

	renderer := NewRenderer()
	variables := renderer.ExtractVariables(body)

	now := time.Now().Unix()
	s := &Skill{
		ID:              skillID,
		Version:         frontmatter.Version,
		DisplayName:     frontmatter.DisplayName,
		Description:     frontmatter.Description,
		Authors:         frontmatter.Authors,
		Tags:            frontmatter.Tags,
		Source:          SkillSourceLocalFile,
		SourceURL:       path,
		IsLocalEditable: false,
		State:           SkillStateEnabled,
		Scope:           finalScope,
		ProjectID:       strings.TrimSpace(frontmatter.ProjectID),
		WorkspaceDir:    workspaceDir,
		Templates: []SkillTemplate{
			{
				Name:       templateName,
				Content:    body,
				Variables:  variables,
				IsRequired: true,
			},
		},
		CreatedAt: now,
		UpdatedAt: info.ModTime().Unix(),
	}
	// 若文件级 project scope 且未显式 project_id，使用传入的 projectID。
	if finalScope == SkillScopeProject && s.ProjectID == "" {
		s.ProjectID = strings.TrimSpace(projectID)
	}
	return s, nil
}

// skillFromParseError 把解析失败的文件也注册为 invalid 状态的 Skill，
// 让用户能在 Manage Skills 看到错误原因，而不是静默忽略。
func skillFromParseError(path, dirName string, scope SkillScope, workspaceDir, projectID string, parseErr error) Skill {
	return Skill{
		ID:              dirName,
		Version:         "1.0.0",
		DisplayName:     dirName,
		Description:     "文件系统 Skill 解析失败: " + parseErr.Error(),
		Source:          SkillSourceLocalFile,
		SourceURL:       path,
		IsLocalEditable: false,
		State:           SkillStateInvalid,
		InvalidReason:   parseErr.Error(),
		Scope:           scope,
		WorkspaceDir:    workspaceDir,
		ProjectID:       projectID,
		CreatedAt:       time.Now().Unix(),
		UpdatedAt:       time.Now().Unix(),
	}
}

// getEnabledScanDirs 从 settings 表读取启用的扫描目录模板；未配置时返回全部默认目录。
func getEnabledScanDirs(settings SettingStore) ([]string, error) {
	if settings == nil {
		return DefaultSkillScanDirs, nil
	}
	val, err := settings.GetSetting("skill_scan_dirs")
	if err != nil || strings.TrimSpace(val) == "" {
		return DefaultSkillScanDirs, nil
	}
	var dirs []string
	if err := json.Unmarshal([]byte(val), &dirs); err != nil {
		return DefaultSkillScanDirs, nil
	}
	// 只保留在 DefaultSkillScanDirs 中存在的目录，防止非法配置。
	valid := make(map[string]bool)
	for _, d := range DefaultSkillScanDirs {
		valid[d] = true
	}
	filtered := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if valid[d] {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		return DefaultSkillScanDirs, nil
	}
	return filtered, nil
}

// broadcast 向事件总线发送 Skill 事件；bus 为 nil 时静默跳过。
func (fl *FileLoader) broadcast(eventType, skillID string, data map[string]any) {
	if fl.bus == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["event_type"] = eventType
	data["id"] = skillID
	fl.bus.SendEvent(data)
}
