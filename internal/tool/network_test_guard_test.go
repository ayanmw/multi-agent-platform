package tool

import (
	"os"
	"testing"
)

// requireRealNetwork 守卫依赖真实外网访问（或真实 API key）的测试。
//
// 这类测试不具备确定性：依赖第三方站点可用性、地域网络策略与反爬策略，
// 在 CI（尤其是境外 runner）上极易失败，因此默认一律跳过。
//
// 两道门：
//   - `go test -short`：始终跳过（CI 使用该模式）；
//   - 未设置 `RUN_NETWORK_TESTS=1`：跳过。
//
// 本地手动验证 provider 时执行：
//
//	RUN_NETWORK_TESTS=1 go test ./internal/tool/ -run TestReal -v
func requireRealNetwork(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过真实网络测试（-short 模式）")
	}
	if os.Getenv("RUN_NETWORK_TESTS") != "1" {
		t.Skip("跳过真实网络测试（设置 RUN_NETWORK_TESTS=1 可启用）")
	}
}
