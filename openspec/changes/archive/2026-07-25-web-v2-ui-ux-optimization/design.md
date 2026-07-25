# web-v2-ui-ux-optimization Design

## Context

web/v2 是一个 Vue 3 + Vite 的单页控制室，没有客户端路由，布局完全由 `useLayout()` 断点驱动。当前桌面端支持拖拽分栏（Sessions dock、主舞台、Files dock、Cron dock），移动端则是单栏 tab 视图。评审发现的问题集中在四个领域：

1. **布局与响应式**：tablet 断点下 CommandBar 的 fixed 定位与 flex 容器冲突；桌面端多面板并行时主舞台被过度挤压；Flyout 在桌面小屏可能溢出。
2. **移动端体验**：主舞台底部缺少对 CommandBar + MobileNav 的空间预留；stage 与侧边栏互斥造成导航摩擦；部分组件 hover-only、触控目标过小。
3. **可访问性**：弹窗缺少焦点捕获/返回；Dock rail / File tree 不是键盘可操作元素；状态指示依赖颜色/emoji；Toast live region 不完整。
4. **视觉一致性**：硬编码色值与 emoji-only 图标破坏主题系统；进度条使用固定假值。

本次变更定位为前端层面的体验打磨，不引入新的后端 API，不修改业务数据模型。

## Goals / Non-Goals

**Goals:**

- 修复 tablet 与 mobile 布局中的真实遮挡/塌陷问题。
- 为桌面端侧边面板建立宽度治理规则，保证主舞台最小可用宽度。
- 将触摸设备上不可用的 hover-only 交互改为 click/tap 可用。
- 补齐关键可访问性缺口（焦点、键盘、ARIA、颜色/图标语义）。
- 统一主题 token 使用，移除显而易见的硬编码色值。
- 修复状态层直接修改 store 对象与假进度条等 UX 负面实现。

**Non-Goals:**

- 不重构整体路由结构（保持无 router 的单页控制室）。
- 不实现完整的 Skill runtime 链路（仅做 UI 层面的降级提示）。
- 不替换整个图标系统；先引入图标库并替换关键 emoji-only 按钮，逐步推进。
- 不改动后端业务逻辑或数据库 schema。

## Decisions

### 1. Tablet 与 Mobile 使用不同的 CommandBar 布局模型

只在 `<768px` 的纯 mobile 下保留 `position: fixed; bottom: 0`，因为 mobile 需要底部导航 + 底部输入栏同时固定。Tablet（768–1023px）下把 CommandBar 放回 flex 流，父容器只按内容高度分配空间，避免 fixed 定位造成的空白塌陷和键盘弹出时的滚动异常。

**替代方案**：统一对所有 `<=1023px` 使用 fixed。已被否决，因为 tablet 空间足够容纳 flex 布局，fixed 反而引入双重高度计算。

### 2. 桌面端侧边面板宽度治理采用“token + 计算折叠”两层策略

第一层用 CSS token 给 CronDockPanel 明确的 `flex-basis`、`min-width`、`max-width`，并用 `max-width` 限制右侧组合区总宽。第二层在 `useLayout` 里计算 `availableStageWidth`，当主舞台宽度低于阈值（建议 600px）时自动折叠 Cron dock，必要时折叠 Files dock，并通过 Toast 提示。

**替代方案**：直接禁止同时打开 Files 和 Cron。已被否决，因为会打断已有桌面工作流；自动折叠更渐进。

### 3. 触控目标统一使用 44px 最小 hit area

扩展 `responsive.css` 的触控规则，覆盖 SessionDock 与 FileTreeNode 中所有交互元素。在行高上统一 `min-height: 44px`，对按钮本身或按钮 wrapper 加 `min-width: 44px`。

**替代方案**：每个组件单独写 media query。已被否决，因为集中规则更易维护且能防止新增组件遗漏。

### 4. ThemePalette 改为 click/tap 主触发、hover 为桌面增强

触屏设备没有可靠 hover，因此 click/tap 是唯一必须路径。桌面保留 mouseenter 预览体验，但面板打开由 click 切换，click-outside 关闭。

**替代方案**：完全删除 hover 增强。已被否决，hover 预览对桌面用户有明确价值，保留可提升效率。

### 5. 弹窗可访问性使用轻量化 focus-trap 组合式函数

新增 `useFocusTrap` composable，基于原生 `Tab` / `Shift+Tab` 和 `focusable` 选择器实现焦点捕获；unmount 时保存并恢复触发元素焦点。不引入额外依赖（如 `focus-trap` npm 包），保持项目零额外依赖倾向。

**替代方案**：引入成熟 npm 包。已被否决，当前弹窗数量有限，自实现足够且避免依赖膨胀。

### 6. 图标库选择推荐 Phosphor

Phosphor 提供一致性高、可配置粗细的 SVG 图标，与现有“控制室/工具感”气质匹配。第一阶段先替换 emoji-only 按钮（TopBar、CommandBar、DockPanel），后续再系统替换所有 emoji。

**替代方案**：Tabler、Heroicons。Tabler 也很合适；Heroicons 风格偏圆润。Phosphor 的线条感更符合当前“工业/工具”氛围。

### 7. CommandBar 进度条优先接入真实 step 进度，否则使用 indeterminate 动画

后端当前未显式返回百分比，但前端已知 `currentStepIndex / maxSteps` 和 `task.status`。因此先按 step 比例估算；若数据缺失则渲染 indeterminate 动画条，不再显示固定 38%。

**替代方案**：直接删除进度条。已被否决，进度指示对长任务有明确安抚作用，indeterminate 是更诚实的降级。

## Risks / Trade-offs

- **[Risk] CSS 改动影响现有拖拽分栏布局** → 在改动 `App.vue` 的 flex 结构后，必须在桌面 1024px、1280px、1920px 以及 iPad 横竖屏模拟器中验证拖拽 resize 仍正常。
- **[Risk] 触控目标放大后部分紧凑列表变长** → SessionDock 项目头部按钮放大后可能折行，需要给按钮 wrapper 增加 `flex-shrink: 0` 或调整间距。
- **[Risk] 图标库替换短期工作量集中在组件模板** → 每次替换一个组件，避免大面积冲突；保持原有 `aria-label`。
- **[Risk] 自动折叠策略可能让用户困惑** → 折叠时触发 Toast 说明“主舞台过窄，已自动收起右侧面板”，并提供一键恢复按钮。
- **[Risk] focus-trap 自实现遗漏 Shadow DOM 或 contenteditable 边界** → 当前项目未使用 Shadow DOM；focusable 选择器覆盖 button、a[href]、input、textarea、select、details、tabindex >=0 的元素足够。

## Migration Plan

本次变更仅涉及前端代码与 CSS，部署方式：

1. 在功能分支 `feat/web-v2-ui-ux-optimization` 上实施（或使用 git worktree 隔离）。
2. 本地分别验证 desktop 1024/1280/1920、tablet 768/1024、mobile 375/414 下的布局。
3. 跑 `web/v2` 单测 `npm run test`（Vitest）。
4. 合并到 main 后，用户刷新浏览器即可获得更新，无需服务端迁移。

回滚策略：CSS 与 Vue 组件改动可通过 `git revert` 回滚；新增 composable 和图标组件不影响数据。

## Open Questions

1. 是否需要为 tablet 横屏提供左右分栏而非上下堆叠？当前先修复遮挡/塌陷，分栏可作为后续增强。
2. 图标库是否统一用 Phosphor？需要设计/产品确认，或调研项目现有字体负载。
3. 后端是否愿意暴露任务百分比字段？若可以，CommandBar 进度条可进一步精确化。
