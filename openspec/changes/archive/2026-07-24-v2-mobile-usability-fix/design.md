## Context

v2 Observable Control Room 的当前布局为桌面端三栏设计（Sessions Dock + 主舞台 + Files Dock），Manage/Cron/Context/Options 等面板通过 TopBar 下拉浮窗或全屏 Dialog 打开。本次 review 发现移动端（<768px）存在三类核心问题：

1. **顶部空间不足**：TopBar 右侧图标过多（Theme、MCP、Recent Mods、Model Prices、Keyboard Tips、Cron、Manage、Version），在 375-414px 宽度下相互挤压甚至溢出。
2. **浮窗/Dialog 定位失效**：`ManageFlyout` 从 TopBar 右下角下拉，`ContextFlyout` 从 CommandBar 的 Context 按钮定位弹出，小屏下易被裁切或无法关闭。
3. **底部输入区与虚拟键盘冲突**：`CommandBar` 在平板/移动端使用 `position: fixed` 贴底，iOS/Android 虚拟键盘弹起时 `100dvh` 收缩，输入条可能遮挡主舞台最新消息；同时 MobileNav 与 CommandBar 叠加，导致非 Stage tab（sessions/files）内容也被输入区遮挡。

约束：
- 必须保持桌面端现有行为不变。
- 必须保持现有组件和 stores 的可测试性。
- 不引入新的运行时依赖（除非用于 CSS/字体，可选）。

## Goals / Non-Goals

**Goals:**
- 在 375px 及以上宽度下，核心用户路径可用：切 session、发任务、看运行状态、打开 Manage/Cron。
- 移动端 TopBar 不溢出，非核心操作可访问且不干扰主任务。
- MobileNav 提供底部直达入口，浮窗改 bottom sheet / 全屏 drawer。
- 输入区在移动端不再 fixed，而是跟随 root flex 布局，并处理虚拟键盘安全区。
- 触控目标尺寸与基础 a11y 属性符合移动体验底线。

**Non-Goals:**
- 不在本次重建完整的 mobile App 体验（如下拉刷新、原生路由手势、PWA）。
- 不改动后端 API。
- 不做重大设计体系换肤（仅 token 一致性清理）。

## Decisions

1. **MobileNav 扩展到 5-tab**
   - 新增 `manage` 和 `cron` tab，把原本藏在 TopBar 的长尾入口下沉到底部。这样用户不必先点 TopBar 男人图标 → 再点展开管理。
   - 替代方案：保持 3-tab 并把 Manage 入口留在 TopBar discard，因为 3-tab 无法承载 Cron，而 Cron 是本次用户明确需要的控制室核心能力。

2. **Stage 外的 tab 隐藏 CommandBar**
   - `activeMobileTab !== 'stage'` 时，App.vue 渲染的 `<CommandBar>` 应当隐藏；Sessions 和 Files 是只读浏览页，不需要输入条。
   - 替代方案：保持固定 CommandBar，但会压缩列表可视区域，且容易误触发送。

3. **ManageFlyout / ContextFlyout 移动端改为 bottom sheet / 全屏面板**
   - 小屏下把这两类浮窗渲染为占满底部或全屏的 Dialog，而不是从 TopBar/CommandBar 绝对定位弹出。
   - 替代方案：在现有 flyout 上增加 left/right clamp；但 flyout 内部表格/表单宽度仍不够，体验差。

4. **CommandBar 小屏不再 fixed**
   - `<1023px` 的 `position: fixed` 改回 flex item，`App.vue` 的 `layout-mobile` 负责整体高度分配；输入框 focus 时主舞台 padding-bottom 动态调整。
   - 替代方案：监听 `visualViewport` 直接 translateY 输入条；更复杂，优先选简单 flex 方案，必要时二阶段补 visualViewport 监听。

5. **TopBar 右侧图标在移动端折叠为一个 "More" 菜单**
   - "More" 按钮弹出 bottom sheet，聚合 Theme/Keyboard/MCP/Prices/Mods；Cron 和 Manage 下沉到 MobileNav 后不再进 More。
   - 替代方案：全部保留右侧图标但做横向滚动；用户更难发现入口。

6. **统一使用 `aria-label` + 增大触控热区**
   - 不等设计系统重构，先在移动端兜底 `min-width/min-height: 44px`；桌面端保持原视觉尺寸（仅 hover 状态）。

## Risks / Trade-offs

- **[Risk]** 修改 App.vue 主布局会影响桌面端回归测试。  
  **Mitigation**：所有桌面断点（≥1024px）与大屏平板（768-1023px）CSS 选择器保持不变，仅新增/调整 `<768px` 规则。

- **[Risk]** 去除 `position: fixed` 后 Android/iOS 键盘弹起会导致 layout 重算，某些浏览器下 `env(safe-area-inset-bottom)` 不够。  
  **Mitigation**：先使用 `100dvh` + content-aware padding；若仍有遮挡，二阶段引入 `visualViewport` 监听做增量修复。

- **[Risk]** 把 Cron/Manage 下沉到底部 tab 后，受 More 菜单影响的按钮需要决定取舍。  
  **Mitigation**：More 菜单保留 MCP/Prices/Keyboard/Mods/Theme，移动端的 VersionSwitcher 仍可在 More 底部显示。

- **[Risk]** 文件树在 mobile 屏幕宽度很窄，小手势容易误操作。  
  **Mitigation**：只做布局容器修复，文件树节点本身至少 44px 高度，暂不做更复杂的拖拽支持。

## Open Questions

- 是否允许在移动端把 `Context Window` 直接做成一个 tab 而不是按钮？暂时保留原按钮入口，优先修复 Manage/Cron。
- 是否引入第三方的 bottom sheet 库？本次不引入，用 Vue Transition + fixed overlay 自研最小实现。
