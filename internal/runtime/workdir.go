// workdir.go — per-run 可变工作目录持有者的接口定义。
//
// 本接口刻意放在 runtime 包而非 workspace 包：runtime 的 Engine 需要读它注入
// tool 的 ExecuteContext.Workdir，但 runtime 不应反向 import workspace
// （workspace 是叶子原语包，runtime 依赖它会形成不必要的耦合，且未来若有
// 非 worktree 的 CWD 切换实现也能直接接入本接口）。
//
// *workspace.WorkdirHolder 天然满足本接口（Get() string）。

package runtime

// WorkdirProvider 是 per-run 可变工作目录的只读接口。
// Engine 在每次 tool 调用前读 Get()，把当前值经 ExecuteContext.Workdir 注入，
// 使其优先于 LLM 传入的 input["workdir"]——这是 worktree 隔离防逃逸的关键：
// worktree/create 改写 holder、worktree/exit 恢复 holder，LLM 无法伪造。
type WorkdirProvider interface {
	// Get 返回当前工作目录绝对路径；空串表示未设置（回退 input["workdir"]）。
	Get() string
}
