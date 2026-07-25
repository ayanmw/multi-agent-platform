# web-v2-ui-ux-optimization Tasks

## 1. Layout & Responsive Foundation

- [x] 1.1 Refactor `App.vue` to separate tablet and mobile CommandBar positioning models: tablet uses flex flow, mobile keeps fixed bottom with stage padding.
- [x] 1.2 Add CSS variables and rules in `App.vue` / `responsive.css` so `.layout-mobile .main-stage` reserves bottom padding for CommandBar + MobileNav + safe-area inset.
- [x] 1.3 Verify tablet (768–1023px) and mobile (<768px) layouts in browser dev tools; ensure no double command-area space.

## 2. Desktop Dock Width Governance

- [x] 2.1 Add explicit `width`, `min-width`, and `max-width` to `CronDockPanel.vue` and its container in `App.vue`.
- [x] 2.2 Define a right-dock-zone combined width cap (`max-width: min(45vw, 720px)`) in `App.vue` desktop layout.
- [x] 2.3 Extend `useLayout.ts` to compute `availableStageWidth` based on current dock widths and resizer widths.
- [x] 2.4 Implement auto-collapse behavior: when `availableStageWidth < 600px`, collapse Cron dock first, then Files dock, with a Toast explaining the action.
- [x] 2.5 Add logic to prevent reopening a dock when there is insufficient space.
- [x] 2.6 Verify at 1024px, 1280px, and 1920px widths with various dock open/close combinations.

## 3. Flyout Positioning Safety

- [x] 3.1 Update `OptionsFlyout.vue` positioning logic: compute `actualWidth` from available viewport space before clamping `left`.
- [x] 3.2 Handle right-edge overflow by flipping flyout to the left side of the trigger when needed.
- [x] 3.3 Apply equivalent edge-safe positioning to `ContextFlyout.vue` if it shares the same pattern.

## 4. Touch Target Expansion

- [x] 4.1 Extend `responsive.css` touch-target rules to cover `.session-action`, `.collapse-btn`, `.project-new-session-btn` and similar SessionDock action classes.
- [x] 4.2 Ensure `SessionDock.vue` action buttons render with at least 44×44px hit area without breaking header layout (add wrapper / flex-shrink controls).
- [x] 4.3 Set `.tree-row` in `FileTreeNode.vue` to `min-height: 44px` and vertically center contents.
- [x] 4.4 Ensure `FileTreeNode.vue` open-in-new-tab button has a 44×44px hit area on mobile.
- [x] 4.5 Run existing Vitest tests and add/adjust any tests for SessionDock / FileTreeNode rendering sizes if feasible.

## 5. Hover-to-Click Adaptation

- [x] 5.1 Convert `ThemePalette.vue` to toggle on click/tap while preserving desktop `mouseenter` preview enhancement.
- [x] 5.2 Implement click-outside and explicit close control for `ThemePalette.vue`.
- [x] 5.3 Make `FileTreeNode.vue` open-in-new-tab button visible on coarse-pointer / mobile devices (`@media (pointer: coarse)` or `isMobile` check).
- [x] 5.4 Restructure `MobileBottomSheet.vue` so the close button lives in a sticky header above the scrollable body.
- [x] 5.5 Verify ThemePalette and FileTreeNode on iOS Simulator / Android Emulator touch targets.

## 6. Dialog Focus Management & ARIA

- [x] 6.1 Create `useFocusTrap.ts` composable that captures Tab/Shift+Tab focus within a dialog and closes on Escape.
- [x] 6.2 Add focus restoration to the triggering element on dialog close.
- [x] 6.3 Apply `useFocusTrap` to Inspector dialog, Case Detail/Form dialog, Approval dialog, and other modal overlays.
- [x] 6.4 Add `role="dialog"`, `aria-modal="true"`, and `aria-labelledby` to all modal dialogs and full-screen drawers.
- [x] 6.5 Make `.dock-rail` in `DockPanel.vue` keyboard operable (`tabindex="0"`, `role="button"`, Enter/Space handling, `aria-expanded`).
- [x] 6.6 Make `.tree-row` in `FileTreeNode.vue` keyboard operable (`tabindex="0"`, `role="treeitem"`, Enter/Space handling).

## 7. Status & Accessibility Improvements

- [x] 7.1 Refactor status chips and role badges to include either visible text labels or visually hidden text conveying state.
- [x] 7.2 Add `aria-atomic="true"` to `Toast.vue` live region.
- [x] 7.3 Audit and ensure all emoji-only buttons have `aria-label` and ideally visible tooltips.
- [x] 7.4 Verify reduced-motion media queries still disable non-essential animations after changes.

## 8. Theme Cleanup

- [x] 8.1 Add theme-derived overlay/glass tokens (`--overlay-bg`, `--panel-gradient-top`, `--glass-bg`) to `themes.css`.
- [x] 8.2 Replace hard-coded `rgba()` values in `ContextWindowPanel.vue` with new theme tokens.
- [x] 8.3 Replace hard-coded hex values in `KeyboardTips.vue` with existing theme tokens.
- [x] 8.4 Choose icon library (recommend Phosphor) and add it to `package.json` / dependencies.
- [x] 8.5 Replace emoji-only buttons in `TopBar.vue` and `CommandBar.vue` with SVG icons from the chosen library, preserving `aria-label`.
- [x] 8.6 Fix `ContextFlyout.vue` resize cursor: use `ew-resize` for width handle, `ns-resize` for height handle, remove global `*` override.
- [x] 8.7 Run full formatter/linter pass to fix formatting drift (e.g., `height:100%;` in `ContextWindowPanel.vue` line 396).
- [x] 8.8 Review all changed components under each theme (obsidian, terminal, amber, dusk, solar, ice, auto).

## 9. State & Progress Cleanup

- [x] 9.1 Add `clearCacheForSession(sessionId)` method to the task store (`useTaskStore.ts` or equivalent).
- [x] 9.2 Replace direct `taskCache.value` mutation in `App.vue` lines 691–703 with a call to the new store method.
- [x] 9.3 Update `CommandBar.vue` progress logic to use real `currentStepIndex / maxSteps` when available.
- [x] 9.4 Implement indeterminate animation progress bar for running/pending states without reliable progress data.
- [x] 9.5 Remove fixed `38%` placeholder progress value entirely.

## 10. Verification & Documentation

- [ ] 10.1 Run `npm run test` in `web/v2` and fix any failing Vitest tests.
- [ ] 10.2 Run `npm run build` in `web/v2` and resolve build/lint errors.
- [ ] 10.3 Manually verify layouts at 375px, 414px, 768px, 1024px, 1280px, 1920px viewports.
- [ ] 10.4 Manually verify keyboard navigation through dialogs, dock rail, and file tree.
- [ ] 10.5 Run axe-core or Lighthouse accessibility audit and address critical issues introduced by changes.
- [ ] 10.6 Update `roadmaps/ROADMAP.md` with this UI/UX optimization entry and any new decisions.
- [ ] 10.7 Run `openspec-verify-change --change web-v2-ui-ux-optimization` and address any failures.
