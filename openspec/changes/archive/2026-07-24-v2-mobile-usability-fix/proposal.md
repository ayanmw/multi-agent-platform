## Why

v2 Observable Control Room 默认在桌面端设计为左 Sessions Dock、中舞台、右 Files Dock 三栏可调布局，并把 Manage/Cron/Context 等都通过 TopBar 下拉或 Dialog 打开。在窄屏/移动设备上，顶部按钮区溢出、浮窗定位失效、底部输入栏固定与虚拟键盘冲突，导致核心任务发送、管理入口访问等关键路径几乎不可用。本项目需要保证移动端至少可完成 "查看会话/发送任务/查看运行状态/管理 Cron" 等基础操作。

## What Changes

- **Mobile 顶部栏重构**：TopBar 右侧图标按钮做小屏折叠/再分组，保留连接状态暴露，避免溢出。
- **Mobile 底部导航扩展**：MobileNav 从 3-tab（stage/sessions/files）扩展到 5-tab，把 Manage/Cron 直达到底部。
- **Manage/Context 浮窗移动端适配**：小屏下改为 bottom sheet / 全屏 drawer，不再作为 TopBar 定位下拉。
- **底部输入区布局修正**：取消 `<1023px` 下 `position: fixed`，改为跟随 flex 容器；移动端键盘弹起时避免遮挡最新内容。
- **触控与可读性兜底**：移动端统一 `min-width/min-height: 44px`，提升 tab 字号，代码块换行。
- **Inspector 全屏 Dialog 手势/关闭优化**：移动端关闭按钮 ≥44px，支持点击 overlay/ESC/返回手势关闭。
- **移动端非 Stage tab 隐藏 CommandBar**：避免 Sessions/Files 内容被输入区遮挡。
- **模块级状态整理**：将 useTaskStore 中模块级副作用（approval timer、WS 引用）移入 store 闭包，避免热更新/多实例泄漏（非需求变更，仅架构卫生）。
- **设计 token 一致性清理**：减少无意义的 fallback 硬编码色值；给图标按钮统一加上 `aria-label`。

## Capabilities

### New Capabilities
- `v2-mobile-layout`: 移动端响应式布局重构（topbar、bottom nav、command area、bottom sheet）。
- `v2-mobile-a11y-touch`: 移动端 a11y 与触控目标最小尺寸规范。

### Modified Capabilities
- （无现有 v2 前端/响应式相关 spec，本次为新增能力 spec）

## Impact

- 受影响的组件：`App.vue`、`TopBar.vue`、`MobileNav.vue`、`CommandBar.vue`、`ManageFlyout.vue`、`ContextFlyout.vue`、`DockPanel.vue`、`SessionDock.vue`、`StepCard.vue`。
- 受影响的 composables：`useLayout.ts`。
- 受影响的样式：`responsive.css`、`global.css`、`themes.css`。
- 不改动后端 API 与现有功能行为。
- 测试：更新/补充 `*.test.ts`（如 MobileNav、CommandBar、App 布局相关测试），并运行 `pnpm test` / `pnpm typecheck`。
