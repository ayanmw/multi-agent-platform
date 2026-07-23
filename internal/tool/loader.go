package tool

import "context"

// ToolLoader 是工具加载抽象。实现者从某种来源（内置、DB、未来 plugin）
// 返回一组 tool.Tool，供 Registry 注册。
//
// 注意：loader 接口本身不依赖 pkg/db，以避免 internal/tool → pkg/db 的
// 反向依赖造成 import cycle。DB 侧加载器可在 cmd/server 中用闭包实现。
type ToolLoader interface {
	Load(ctx context.Context) ([]Tool, error)
}

// RecordLoader 是 DBToolLoader 使用的最小记录加载函数签名。
// 定义在 tool 包内以避免直接依赖 pkg/db。
type RecordLoader func() ([]map[string]any, error)

// DBToolLoader 从 RecordLoader 加载持久化的动态工具。
// 调用方负责把 pkg/db.ToolRecord 转换为 map[string]any。
type DBToolLoader struct {
	load RecordLoader
}

// NewDBToolLoader 创建 DBToolLoader。load 函数通常由 cmd/server 注入，
// 内部调用 db.QueryToolsV2 并把 ToolRecord 转为 map。
func NewDBToolLoader(load RecordLoader) *DBToolLoader {
	return &DBToolLoader{load: load}
}

// Load 从 loader 读取工具记录并转换为 DynamicTool。
// 记录 map 必需字段：name(string)、description(string)、parameters(map)、
// execution_config(map)。可选：namespace(string)、version(string)、source(string)。
func (l *DBToolLoader) Load(_ context.Context) ([]Tool, error) {
	records, err := l.load()
	if err != nil {
		return nil, err
	}
	out := make([]Tool, 0, len(records))
	for _, rec := range records {
		desc := ToolDescriptor{
			Namespace:       getString(rec, "namespace", ""),
			Name:            getString(rec, "name", ""),
			Version:         getString(rec, "version", ""),
			Source:          ToolSource(getString(rec, "source", "local_db")),
			Description:     getString(rec, "description", ""),
			Parameters:      getMap(rec, "parameters"),
			ExecutionConfig: getMap(rec, "execution_config"),
		}
		if desc.Source == "" {
			desc.Source = ToolSourceLocalDB
		}
		out = append(out, NewDynamicToolFromDescriptor(desc))
	}
	return out, nil
}
