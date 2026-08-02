# Makefile —— 统一构建/测试入口（替代零散脚本）
#
# 约定：
#   - 每条 recipe 都在独立的子 shell 中运行，且以 `make` 启动时的目录为基准。
#     因此 build-ui 里的 `cd web` 不会泄漏到 build 目标，build 无需也不能加 cd。
#   - build-ui 写成一条自包含链，确保第二次 cd 相对第一次 cd 之后的目录。
#   - Windows 下 mock 回归必须 PYTHONUTF8=1（见 scripts/cases-regression.sh 注释）。

GO_BIN    := bin/server
UI_V1     := web
UI_V2     := web/v2

.PHONY: all build build-ui test vet fmt fmt-check smoke regression multi-smoke ci clean

all: build

build:                      ## 仅构建 Go 二进制（不含前端；前端需先 make build-ui）
	go build -o $(GO_BIN) ./cmd/server

build-ui:                  ## 构建 v1 + v2 前端（go:embed 进二进制）
	cd $(UI_V1) && npm install && npm run build && cd ../$(UI_V2) && npm install && npm run build

test:                      ## Go 单元测试（全模块）
	go test ./...

vet:                       ## 静态检查（不阻断，仅报告）
	go vet ./... || true

fmt:                       ## 格式化（gofmt -w，作用于 internal/cmd/pkg）
	gofmt -w internal cmd pkg

fmt-check:                 ## 格式检查（仅报告未格式化文件，不阻断）
	@gofmt -l internal cmd pkg || true

smoke:                     ## 后端冒烟测试
	bash scripts/smoke-test.sh

regression:                ## 21 case mock 回归（确定性，无需 API key）
	PYTHONUTF8=1 LLM_USE_MOCK=true bash scripts/cases-regression.sh

multi-smoke:               ## 多 Agent 编排 mock 冒烟
	PYTHONUTF8=1 LLM_USE_MOCK=true bash scripts/multi-agent-smoke.sh

# 本地等价 CI 门禁：格式检查(报告) + vet(报告) + 测试 + 构建。
# 不含前端与耗时回归；gofmt/vet 当前为 warn-only（存量 125 文件未格式化）。
ci: fmt-check vet test build

clean:
	rm -f $(GO_BIN)
