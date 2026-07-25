# web-v2-ui-ux-optimization Proposal

## Why

web/v2 控制室在移动端、平板端和部分桌面窄屏场景下存在布局冲突、触控目标不足、可访问性缺口以及主题一致性瑕疵。随着项目进入 Phase 8-B 收尾与后续生产化阶段，这些体验问题会直接影响真实用户（尤其是手机/平板用户和键盘/读屏用户）对“白盒 Agent”控制室的第一印象。因此需要一次系统性的 UI/UX 打磨变更，补齐响应式、可访问性和视觉一致性方面的短板。

## What Changes

- 修复 tablet 端 CommandBar `position: fixed` 与 flex 容器的高度冲突，并重新校准移动端主舞台底部偏移，避免时间线内容被底部栏遮挡。
- 给 CronDockPanel 补充固定宽度与 min/max 约束，为桌面端右侧面板组合增加总宽度上限，并在窄桌面自动折叠侧边面板以保护主舞台最小宽度。
- 修复 OptionsFlyout / ContextFlyout 在桌面小屏下的边缘溢出定位逻辑。
- 放大 SessionDock 与 FileTreeNode 的触控目标，移除 FileTreeNode“仅在 hover 显示”的按钮限制，让触摸设备可用。
- 将 ThemePalette 从 hover-only 切换为 click/tap 为主、hover 为辅的触发模型；让 MobileBottomSheet 的关闭按钮保持 sticky，避免短视口顶出屏幕。
- 补齐可访问性：弹窗焦点捕获与焦点返回、Dock rail / File tree 键盘支持、Toast `aria-atomic`、状态 chip 不依赖单一颜色/emoji、弹窗统一 `role="dialog"`。
- 清理视觉/主题一致性债务：硬编码色值（KeyboardTips、ContextWindowPanel）、emoji-only 图标改为统一图标库、ContextFlyout resize 光标方向修正、代码格式漂移修复。
- 修复状态层对 UX 的直接影响：App.vue 不再直接 mutate `taskCache`，CommandBar 进度条使用真实进度或 indeterminate 动画替代固定 38% 的占位值。

## Capabilities

### New Capabilities

- `v2-tablet-layout`: tablet 断点下 CommandBar 与主舞台的安全布局模型。
- `v2-dock-width-governance`: 桌面端 Files/Cron dock 宽度约束、组合上限与自动折叠策略。
- `v2-touch-target-expansion`: SessionDock / FileTreeNode 等组件的 44px 触控目标补齐。
- `v2-hover-to-click`: ThemePalette、FileTreeNode 等 hover-only 组件的触摸适配。
- `v2-dialog-focus-trap`: 弹窗焦点捕获、焦点返回与键盘关闭。

### Modified Capabilities

- `v2-mobile-layout`: 扩展移动端主舞台底部偏移要求；调整 CommandBar 在 mobile 的布局流为 flex 项并预留底部 padding。
- `v2-mobile-a11y-touch`: 扩展触控目标范围到 SessionDock / FileTreeNode；补充状态指示不依赖单一颜色/emoji 的要求。

## Impact

- 主要影响 `web/v2/src/App.vue`、`CommandBar.vue`、`SessionDock.vue`、`FileTreeNode.vue`、`ThemePalette.vue`、`MobileBottomSheet.vue`、`CronDockPanel.vue`、`OptionsFlyout.vue`、`ContextFlyout.vue`、`DockPanel.vue`、`Toast.vue`、`KeyboardTips.vue`、`ContextWindowPanel.vue` 等组件，以及 `useLayout.ts`、相关 store、CSS 主题 token。
- 不改动后端 API 契约，不引入新的 REST/WebSocket 协议字段。
- 可能需要选定一套统一图标库（推荐 Phosphor 或 Tabler），属于设计决策。
- 若后续补齐 Skill 完整链路，需要后端配合，但本次 UI/UX 变更不阻塞该工作。
