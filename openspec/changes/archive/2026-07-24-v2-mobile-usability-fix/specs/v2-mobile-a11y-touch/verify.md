## Verification

### Automated checks

- `npx vue-tsc -b --noEmit` in `web/v2` passes with no type errors.
- `npx vitest run` in `web/v2` passes: 12 test files, 128 tests.

### Manual a11y / touch checks

- All buttons in `TopBar.vue` (desktop and mobile) have `aria-label` attributes.
- All emoji-only buttons in `CommandBar.vue` have `aria-label` attributes (Options, Context, Case Library, Pause, Cancel, Send).
- All menu items in `ManageFlyout.vue` have `aria-label` attributes.
- `MobileBottomSheet.vue` close button has `aria-label="Close"`.
- `responsive.css` enforces `min-width: 44px; min-height: 44px` for buttons and `[role="button"]` elements on mobile (`max-width: 767px`).
- Mobile bottom navigation tab labels are `12px` or larger and hit targets are `44×44px`.
- `StepCard.vue` `.tool-code` uses `white-space: pre-wrap; word-break: break-word;` on mobile to prevent horizontal scroll.
- `MobileBottomSheet.vue` and flyout transitions respect `prefers-reduced-motion: reduce` by setting short durations / disabling transforms.

### Problems recorded

- `StepCard.vue` expand transition height is capped at `320px` with `overflow: hidden`; on mobile this may clip very long inputs/outputs. Kept at `max-height: 160px` for `.tool-code` to balance readability.
- ThemePalette inside More sheet is a compound control; single `aria-label` not applied at wrapper level, but inner interactive controls (swatches) are native buttons. Future work: add an `aria-label` to the ThemePalette fieldset/legend.
