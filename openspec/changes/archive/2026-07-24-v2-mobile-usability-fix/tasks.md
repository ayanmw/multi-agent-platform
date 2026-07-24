## 1. Foundation & Layout State

- [ ] 1.1 Extend `useLayout.ts` active mobile tab union to `'stage' | 'sessions' | 'files' | 'manage' | 'cron'`.
- [ ] 1.2 Add `isCommandBarVisible` derived state to `useLayout.ts`: false on mobile when active tab is not `stage`.
- [ ] 1.3 Add `mobileMoreOpen` / `mobileManageOpen` / `mobileCronOpen` reactive flags to `useLayout.ts` (or local state in App.vue).
- [ ] 1.4 Add helper class constants in `responsive.css`: `.hidden-command-bar-mobile`, `.mobile-bottom-sheet`, `.mobile-more-sheet`.

## 2. TopBar Mobile Collapse

- [ ] 2.1 Add `isMobile` prop/composable usage in `TopBar.vue`.
- [ ] 2.2 On mobile, hide ThemePalette/Cron/MCP/Mods/Prices/Keyboard/Version right-side icons and render a single "More" icon button.
- [ ] 2.3 Emit `open-mobile-more` event; keep manage/cron logic emit unchanged for desktop.
- [ ] 2.4 Ensure all icon-only buttons have `aria-label` and mobile hit area ≥44px.
- [ ] 2.5 Add/update `TopBar.test.ts` if it exists (or inline coverage in existing component test).

## 3. MobileNav Extension

- [ ] 3.1 Extend `MobileNav.vue` tabs list to include `manage` and `cron`.
- [ ] 3.2 Update tab type usage to match new union.
- [ ] 3.3 Increase label font size to ≥12px and ensure tab hit target ≥44×44px.
- [ ] 3.4 Update `MobileNav.test.ts` snapshots/expectations.

## 4. Bottom Sheet Components for Mobile

- [ ] 4.1 Create reusable `MobileBottomSheet.vue` (slide-up from bottom, full-screen variant, close on overlay/ESC).
- [ ] 4.2 Refactor `ManageFlyout.vue` to use `MobileBottomSheet` when `isMobile` is true; keep desktop dropdown intact.
- [ ] 4.3 Refactor `ContextFlyout.vue` to use `MobileBottomSheet` when `isMobile` is true; keep desktop anchored flyout intact.
- [ ] 4.4 Add `aria-label`/focus-trap basics to bottom sheet close controls.
- [ ] 4.5 Add unit tests for `MobileBottomSheet.vue`.

## 5. CommandBar Mobile Layout

- [ ] 5.1 Remove/override `position: fixed` for `.command-bar` in `CommandBar.vue` media query (<1023px); keep fixed behavior only for desktop if any.
- [ ] 5.2 In `App.vue`, mobile layout wraps CommandBar inside the flex center-column and conditionally renders it only when command bar should be visible.
- [ ] 5.3 Update `App.vue` mobile bottom padding/margin calculations so the main stage is not overlapped when the keyboard opens.
- [ ] 5.4 Ensure textarea command-input on mobile has min-height 48px and prevents iOS zoom (`font-size: 16px`).
- [ ] 5.5 Run `pnpm typecheck`.

## 6. Inspector Dialog Mobile Optimization

- [ ] 6.1 In `App.vue` inspector dialog styles (<767px), set border-radius to 0 and full viewport.
- [ ] 6.2 Increase `inspector-dialog-close` button to at least 44×44px on mobile.
- [ ] 6.3 Ensure overlay click closes inspector dialog.
- [ ] 6.4 Add Escape key listener to close inspector dialog.

## 7. Content Readability & Token Cleanup

- [ ] 7.1 Update `StepCard.vue` `.tool-code` to wrap on mobile (`white-space: pre-wrap`) and reduce max-height to 160px.
- [ ] 7.2 Review and strip unnecessary fallback hex colors in `.vue`/`.css` files touched (keep fallback only for SSR-critical variables).
- [ ] 7.3 Add `aria-label` to remaining emoji-only buttons in `CommandBar.vue`, `TopBar.vue`, and `ManageFlyout.vue`.
- [ ] 7.4 Ensure `responsive.css` touch-target rule enforces `min-width: 44px; min-height: 44px` for buttons/roles on mobile.

## 8. Verification

- [ ] 8.1 Run `pnpm test` and fix regressions.
- [ ] 8.2 Run `pnpm typecheck` and fix type errors.
- [ ] 8.3 Manual mobile emulation (Chrome DevTools 375px/414px / iPhone SE / Pixel 5) for:  
  - TopBar 不溢出  
  - More 菜单可开可关  
  - 5-tab 切换正常、非 stage tab 隐藏 CommandBar  
  - Manage/Cron bottom sheet 不越界、点 overlay 关闭  
  - Context 浮窗移动端正常打开  
  - 任务发送流程可用  
- [ ] 8.4 Run `openspec verify-change v2-mobile-usability-fix` when artifacts are complete.
- [ ] 8.5 Git commit with message: `Phase UI-v2: mobile usability fix`.
