## Verification

### Automated checks

- `npx vue-tsc -b --noEmit` in `web/v2` passes with no type errors.
- `npx vitest run` in `web/v2` passes: 12 test files, 128 tests.
- `MobileBottomSheet.test.ts` covers open/closed render, title, overlay click, close button, ESC key.

### Manual mobile emulation

Verified under Chrome DevTools device emulation:

| Device / Width | TopBar overflow | 5-tab switch | More sheet | Manage/Cron tab | CommandBar hide | Inspector close |
|----------------|-----------------|--------------|------------|-----------------|-----------------|-----------------|
| iPhone SE 375×667 | ✅ no overflow | ✅ | ✅ opens/closes | ✅ full screen | ✅ hidden on non-stage | ✅ 44×44 close, overlay closes |
| iPhone 12/13 390×844 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Pixel 5 393×851 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| iPhone 14 Pro Max 430×932 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

Key findings recorded during implementation:

1. **PNPM `typecheck` blocked by ignored build scripts.**
   - Symptom: `pnpm typecheck` throws `[ERR_PNPM_IGNORED_BUILDS]`.
   - Resolution: run `npx vue-tsc -b --noEmit` and `npx vitest run` directly, bypassing pnpm's install-time check.
2. **TopBar dynamic emit typing.**
   - Symptom: `emit(action.emit)` cannot resolve union event names at runtime.
   - Resolution: introduce `MoreActionId` union and a dispatch map in `TopBar.vue`.
3. **ManageFlyout EventTarget typing.**
   - Symptom: `e.target?.parentElement` fails because `EventTarget` lacks `parentElement`.
   - Resolution: explicitly narrow target to `Element` / `Node`.
4. **MobileBottomSheet Teleport tests.**
   - Symptom: content is portaled to `<body>` and not inside wrapper element.
   - Resolution: mount with `attachTo: document.body` and query `document.querySelector`.

### Problems not yet fixed

- `ContextFlyout` on mobile uses the same `contextAnchorRect` desktop anchor; on very small landscape heights the bottom sheet may need a `max-height: 80vh` clamp (future polish).
- Long code blocks in `StepCard.vue` now wrap, but JSON with very long keys can still produce vertical overflow; accepted for v1.
