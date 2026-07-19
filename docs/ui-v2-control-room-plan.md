# UI v2 — Observable Control Room 设计方案

> 状态：实施中（骨架 + 核心连线完成，颜色 token 统一，待端到端验证）  
> 分支：`ui-v2-control-room`  
> 工作目录：`D:\Claude-Code-MultiAgent\.claude\worktrees\ui-v2-control-room`  
> 日期：2026-07-19 起

---

## 1. 背景与目标

当前 `web/` 前端已实现完整功能，但存在以下可用性问题：

1. 信息扁平化：sidebar 280px 内塞入 Project / Session / workspace / token / timestamp / delete / rename，认知负担大。
2. 工作区垂直堆叠：聊天输入、turn list、case 库、最终结果全部单列堆叠，多 Agent 对比困难。
3. 面板互相覆盖：AgentConfig / ProjectConfig / Memory / ContextWindow 用替换或 overlay，无法边看执行边查配置。
4. 缺乏响应式：当前布局不适合手机网页访问。
5. 新版本 Skill 系统缺少可视化入口。

### 设计目标

- 以**"可观测控制室 / Observable Control Room"**为概念，把界面从"聊天对话框"升级为"多 Agent 飞行驾驶舱"。
- 在 `web-v2/` 中实现新 UI，**与老 `web/` 共存**，可随时切换、独立构建。
- 桌面端采用**三栏 Dock 布局**，移动端采用**底部 3-tab 布局**。
- 所有 API 调用保持与 backend 兼容，不改动 Go 代码（除非必要的静态资源路由开关）。
- 引入 Skill 系统可视化入口。

---

## 2. 设计方向

### 2.1 风格关键词

- **Industrial utilitarian / deep-space dark**（工业实用 / 深空暗色）：工业精密感 + 深邃暗舱
- **Sharp data accents**（锐利数据强调色）：锐利信息荧光
- 每个 Agent 是一条航迹，每个 step / tool call 是一次仪表读数。

### 2.2 设计系统

```css
:root {
  /* 舱壁黑 */
  --bg-canvas: #0B0D10;
  --bg-panel: #11141A;
  --bg-elevated: #181C24;
  --bg-hover: #202632;

  /* 边框 */
  --border-subtle: rgba(255, 255, 255, 0.06);
  --border-default: rgba(255, 255, 255, 0.10);
  --border-active: rgba(0, 229, 255, 0.40);

  /* 文字 */
  --text-primary: #E8EBF0;
  --text-secondary: #9AA3B2;
  --text-muted: #5C6675;

  /* 状态荧光 */
  --accent-running: #00E5FF;
  --accent-success: #39FF14;
  --accent-warning: #FFB800;
  --accent-danger: #FF4D4D;
  --accent-tool: #A855F7;
  --accent-skill: #FF6B35;

  /* 字体 */
  --font-display: 'Chakra Petch', 'Space Grotesk', sans-serif;
  --font-mono: 'JetBrains Mono', 'Fira Code', Consolas, monospace;
}
```

### 2.3 字体策略

- 标题 / 面板标签：`Chakra Petch`（科技、控制室、锐利）
- 正文 / 代码 / 数据：`JetBrains Mono`（开发者友好）
- 通过 Google Fonts CDN 引入，失败时回退系统无衬线/等宽字体。

---

## 3. 布局架构

### 3.1 桌面端（>=1024px）

```
┌──────────────────────────────────────────────────────────────────────┐
│ Top Bar (logo + status + tools + v1/v2 switch)                       │
├──────────┬───────────────────────────────┬────────────────────────────┤
│ Session  │                               │    Inspector Panel         │
│ Dock     │     Main Stage                │    (resizable / optional)  │
│ (left)   │     - Turn Timeline           │    - Tabs                  │
│          │     - Agent Tracks            │                            │
├──────────┴───────────────────────────────┴────────────────────────────┤
│ Command Bar (bottom fixed)                                            │
└──────────────────────────────────────────────────────────────────────┘
```

- **左侧 Session Dock**：项目选择 + 会话列表 + 新建入口。
- **中间 Main Stage**：时间线轨道，一个 Turn 一条轨道，轨道内多 Agent 泳道并行。
- **右侧 Inspector Panel**：常驻 Dock，Tab 切换 Memory / RAG / Agent Messages / Context / Cases / Agent Config / Project / Skills / Traces。
- **底部 Command Bar**：输入框 + 快捷控制 + options 抽屉。

### 3.2 平板端（768px–1023px）

- Inspector 面板默认隐藏，通过顶部按钮或向右滑出。
- Session Dock 可折叠为窄栏（只显示图标/状态色带）。
- Command Bar 保持底部。

### 3.3 手机端（<768px）

采用底部 3-tab：

```
┌─────────────────────────┐
│ Top Bar (compact)       │
├─────────────────────────┤
│                         │
│    Main Content Area    │
│                         │
├─────────────────────────┤
│ [Stage] [Inspector] [Sessions] │
└─────────────────────────┘
```

- **Stage tab**：当前会话时间线 + 底部浮动 Command Bar。
- **Inspector tab**：右侧面板全部内容。
- **Sessions tab**：左侧 dock 内容。

---

## 4. 组件清单

### 4.1 新增核心组件

| 组件 | 路径 | 职责 |
|------|------|------|
| DockPanel | `components/DockPanel.vue` | 可折叠侧边面板容器（左/右/底） |
| TopBar | `components/TopBar.vue` | 顶部状态栏 + 工具图标 + v1/v2 切换 |
| CommandBar | `components/CommandBar.vue` | 底部命令输入条（原 TaskInput 升级） |
| TimelineTrack | `components/TimelineTrack.vue` | 一个 Turn 的轨道容器 |
| AgentLane | `components/AgentLane.vue` | 单个 Agent 的泳道 |
| StepCard | `components/StepCard.vue` | think / tool_call / observation 卡片 |
| InspectorTabs | `components/InspectorTabs.vue` | Inspector 面板 tab 容器 |
| SkillPanel | `components/SkillPanel.vue` | Skill 系统可视化入口 |
| MobileNav | `components/MobileNav.vue` | 移动端底部 tab 导航 |

### 4.2 复用/改造组件

| 组件 | 处理方式 |
|------|---------|
| App.vue | 完全重写布局骨架 |
| TaskInput.vue | 拆出 CommandBar，保留选项逻辑 |
| TurnList.vue / TurnItem.vue | 重写为 TimelineTrack + AgentLane |
| AgentTree.vue | 重写为 StepCard 流 |
| MetricsPanel.vue | 内容简化后嵌入 TopBar / Inspector header |
| MemoryBrowser.vue / RAGPreviewPanel.vue / ContextWindowPanel.vue / AgentBusTimeline.vue | 直接复用逻辑，调整外壳样式适配新主题 |
| AgentConfig.vue / ProjectConfig.vue / CaseCard.vue | 改造为 Inspector 面板内容 |
| MCPServerDialog / RecentModsDialog / ModelPricesDialog / ApprovalDialog / KeyboardTips | 复用，统一在新主题下轻微调整 |

### 4.3 新增 composable

| Composable | 职责 |
|-----------|------|
| `useLayout.ts` | 响应式断点、dock 折叠状态、移动端当前 tab |
| `useSkills.ts` | 拉取 skill 列表、触发 skill、监听结果 |

---

## 5. 路由与构建策略

### 5.1 目录隔离

- 新 UI 代码放在 `web-v2/`，与 `web/` 同级。
- `web-v2/package.json` 独立，可使用与 `web/` 不同的依赖版本。
- 构建产物输出到 `web-v2/dist/`。

### 5.2 Go 端切换方案

#### 方案 A（推荐）：环境变量 + embed 开关

在 `cmd/server/main.go` 或 `web/embed.go` 中增加环境变量 `UI_VERSION=v2`：

```go
//go:embed dist
var DistV1 embed.FS

//go:embed dist
var DistV2 embed.FS

func WebFS() fs.FS {
    if os.Getenv("UI_VERSION") == "v2" {
        fs, _ := fs.Sub(DistV2, "dist")
        return fs
    }
    fs, _ := fs.Sub(DistV1, "dist")
    return fs
}
```

> 实际实现时需谨慎处理 embed 路径。初步做法：先把 `web-v2/dist` 通过 symlink 或在 build script 阶段复制到 Go 可见目录，再 embed。

#### 方案 B：dev server proxy 切换

开发阶段只用 Vite dev server，通过修改 `vite.config.ts` 的 proxy 目标即可切换 v1/v2，无需改 Go。

### 5.3 当前阶段做法

本次设计先在 `web-v2/` 内完成独立 Vue 项目并可通过 `npm run dev` 预览，暂不在 Go 中做运行时切换（可在文档中预留）。最终验收前再按需加环境变量切换。

---

## 6. Skill 系统入口设计

由于 `main` 已引入 skill 系统，v2 UI 需要：

1. **Skill 列表**：展示可用 skill（通过 backend 路由或硬编码常用 skill 清单）。
2. **Skill 卡片**：显示名称、触发命令（如 `/graphify`）、描述、是否支持。
3. **快速触发**：在 Command Bar 输入 `/skill-name` 时直接调用对应 skill；或点击卡片触发。
4. **结果展示**：Skill 执行结果在 Main Stage 以独立 message / timeline 形式呈现。

### Skill Panel 原型结构

```
┌─ Skills ──────────────────┐
│ /graphify   Knowledge graph│
│ /verify     Verification   │
│ /research   Deep research  │
│ ...                        │
└────────────────────────────┘
```

---

## 7. 动画与微交互

### 7.1 必须动效

- 新建 step 入场：从左侧滑入 + 透明度渐变（staggered，间隔 40ms）。
- Agent 状态指示灯：running 时呼吸脉冲；paused 时琥珀闪烁；failed 时红色高亮。
- 工具调用展开：高度过渡 + 代码块 highlight。
- Command Bar focus：边框发光动画。
- Inspector tab 切换：内容淡入淡出。

### 7.2 性能约束

- 优先 CSS 动画，复杂列表使用 `transform` / `opacity`。
- 大量 step 时开启虚拟滚动（后续迭代）。

---

## 8. 开发计划

| 阶段 | 任务 | 输出 |
|------|------|------|
| 1 | 在 `web-v2/` 初始化 Vite + Vue3 + TypeScript 项目，复用原项目配置 | `web-v2/` 可 `npm run dev` |
| 2 | 建立 design system：`global.css` + tokens + fonts + utilities | 统一变量、字体加载 |
| 3 | 搭建布局骨架：`App.vue` + `TopBar` + `DockPanel` + `CommandBar` + 响应式 | 桌面/移动布局可用 |
| 4 | 实现核心组件：`TimelineTrack` + `AgentLane` + `StepCard` | 静态时间线可渲染 |
| 5 | 接入 WebSocket + TaskStore：复用 `useWebSocket.ts` / `useTaskStore.ts` | 实时任务可运行 |
| 6 | 接入 Session/Project/Agent/Memory/Cases：复用并改造原 composables | 全部 Inspector tabs 可用 |
| 7 | Skill Panel 与 Command Bar skill 触发 | skill 可视化入口 |
| 8 | 响应式与移动端打磨 | 手机端可用 |
| 9 | 构建验证 + 文档更新 | `npm run build` 通过，文档同步 |

---

## 9. 与 `web/` 共存的注意点

1. 不删除 `web/` 任何文件。
2. 不改动 backend 路由逻辑（除非最后加可选 UI 切换环境变量）。
3. `web-v2/src/types/events.ts` 可与 `web/src/types/events.ts` 内容一致或视情况扩展。
4. `web-v2` 可以复用 backend 的 mock / data / storage，启动时指定不同端口或独立运行 dev server。

---

## 10. 验收标准

- [x] `web/v2` 可 `npm run dev` 并连接 backend WebSocket / API。
- [x] 桌面端呈现三栏控制室布局。
- [x] 手机端呈现底部 3-tab 布局。
- [x] 可发起 chat / multi-agent / case / skill 任务。（`handleSend` 解析 `/skill-id ` 前缀 → `enableSkill` → `startTask` / `startTurn` / `startMultiAgentTask`；`handleRunCase` → `startTaskWithCase`）
- [x] 可实时观察 step / tool call / observation / final result。（`useTaskStore` WebSocket 事件已接入，`TimelineTrack` / `AgentLane` / `StepCard` 渲染）
- [x] Inspector 可切换 Memory / RAG / Context / Cases / Agent Config / Project / Skills / Traces。（Tabs + 真实组件全部接入；Cases tab 的 view/edit/save 已接 `CaseDetailModal` + `CaseForm`）
- [x] `npm run build` 无 TypeScript / Vue 错误。（v1 `web/dist` 与 v2 `web/v2/dist` 均构建通过，`go build ./cmd/server` 通过）
- [x] 不破坏 `web/` 原有构建。
- [x] 颜色 token 统一：Case 系列组件、TopBar / DockPanel / MobileNav 的 v1 硬编码颜色与 hex fallback 全部迁移到 v2 CSS variables（新增 `--text-on-accent`）。
- [ ] 端到端冒烟：真实启动 backend（`UI_VERSION=v2`）跑通 chat / case / skill / multi-agent 四类任务，桌面三栏与移动 3-tab 均正常。（留作后续测试阶段）

---

## 10.1 实施进度（2026-07-19）

### 已完成
- **基础架构**：`web/embed.go` 同时 embed `web/dist`（v1）与 `web/v2/dist`（v2）；`cmd/server/main.go` 通过 `UI_VERSION=v2` 环境变量运行时切换静态资源，默认 v1。
- **设计系统**：`global.css` deep-space dark + industrial 主题；`responsive.css` 桌面/平板/移动适配；CSS tokens（bg/border/text/accent/font/space/radius）。
- **布局组件**：`TopBar` / `DockPanel` / `SessionDock` / `CommandBar` / `MobileNav` / `TimelineTrack` / `AgentLane` / `StepCard` / `StatusIndicator` / `InspectorTabs` / `InspectorContent`。
- **Store composables**：`useTaskStore` / `useSessionStore` / `useAgentStore` / `useProjectStore` / `useCaseStore` / `useMemoryStore` / `useContextWindow` / `useTraceStore` / `useToast` / `useRecentMods` / `useModelPrices` / `useMCPStore` / `useKeyboard` / `useLayout` / `useSkills`。
- **后端 API 连线**：WebSocket 事件驱动；Skill 真实 API（`GET /api/skills?source=built_in`、`POST /api/skills/:id/enable`）与 `/skill-id ` 前缀解析；multi-agent `/api/multi-agent`；`startTaskWithCase`；会话/任务历史加载；审批对话框。
- **Inspector 全 tab 接入**：Memory / RAG / Context（含子任务选择）/ AgentConfig / ProjectConfig / SkillPanel / Traces（最小树）/ Sessions 概览 / Cases 列表。
- **Cases tab 完整功能**：`CaseDetailModal`（查看）/ `CaseForm`（新建 + 编辑）/ `CaseCard` Run / tag 筛选；`handleCaseSave` 走 `createCase` / `updateCase`，toast 反馈。
- **颜色 token 统一**：CaseForm / CaseDetailModal / CaseCard / CaseFilter 全量迁移到 v2 token；TopBar / DockPanel / MobileNav 去除 hex fallback；新增 `--text-on-accent` 供 Run/Save 按钮文字用。

### 待进行（后续测试阶段）
- 端到端冒烟验证（`UI_VERSION=v2` 启动 + 四类任务跑通 + 移动端实机/DevTools 模拟）。
- Traces tab 从最小列表升级为可折叠树（当前仅拍平展示）。
- 移动端交互细节复核（底部 sheet、tab 切换、CommandBar 不被遮挡）。
- 用户满意后将 `ui-v2-control-room` 合并回 `main`。

---

## 11. Mobile adaptations

针对手机屏幕（<768px）做了以下打磨：

1. **新增 `web/v2/src/styles/responsive.css`**：在 `main.ts` 中后于 `global.css` 导入，提供 `.hidden-mobile/.hidden-tablet/.hidden-desktop` 工具类；文件内对 `.app-shell`、`.main-stage`、`.mobile-tab-view`、`.dock-panel`、`.command-bar` 等小屏场景做精细布局与安全区适配，并统一加入 `prefers-reduced-motion` 媒体查询。
2. **MobileNav 仅在小屏显示**：组件内部读取 `useLayout.isMobile`，非手机端不渲染；当前 tab 通过 `--accent-running` 高亮，底部 fixed 定位，与 CommandBar 不重叠。
3. **CommandBar 移动端改造**：
   - 选项抽屉在手机端变为底部 modal sheet（`.command-options--sheet`），带可点击 handle。
   - 按钮在空间不足时自动 wrap，textarea 最小高度提升到 48px 并把 font-size 设为 16px 防止 iOS 缩放。
4. **TimelineTrack / AgentLane / StepCard 移动端可用性**：减小 padding/font-size；AgentLane 保持单列垂直堆叠；StepCard code block 改为 `white-space: pre` 并允许水平滚动。
5. **DockPanel 移动端全屏 overlay**：在 `mobile-tab-view` 内部绝对定位填充，无边框圆角，避免 `fixed-width` 被裁剪。
6. **global.css 收尾**：补全 `:root` fallback、添加 `prefers-reduced-motion`、追加 touch-friendly 辅助类。

---

*本计划将在实施过程中根据反馈迭代。*
