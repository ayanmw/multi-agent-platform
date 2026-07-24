import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import MobileBottomSheet from './MobileBottomSheet.vue'

/**
 * MobileBottomSheet 单元测试
 * 验证：open 受控渲染、标题展示、关闭按钮与 ESC 关闭事件。
 */
describe('MobileBottomSheet', () => {
  it('should not render when open is false', () => {
    const wrapper = mount(MobileBottomSheet, {
      props: { open: false, title: 'Demo' },
      attachTo: document.body,
    })
    expect(document.querySelector('.mobile-sheet-overlay')).toBeNull()
    wrapper.unmount()
  })

  it('should render panel and title when open is true', async () => {
    const wrapper = mount(MobileBottomSheet, {
      props: { open: true, title: 'Test Sheet' },
      attachTo: document.body,
    })
    await nextTick()
    expect(document.querySelector('.mobile-sheet-overlay')).not.toBeNull()
    const title = document.querySelector('.mobile-sheet-title')
    expect(title?.textContent).toBe('Test Sheet')
    wrapper.unmount()
  })

  it('should emit update:open false when clicking overlay', async () => {
    const wrapper = mount(MobileBottomSheet, {
      props: { open: true, title: 'Test' },
      attachTo: document.body,
    })
    await nextTick()
    const overlay = document.querySelector('.mobile-sheet-overlay')
    expect(overlay).not.toBeNull()
    ;(overlay as HTMLElement).click()
    await nextTick()
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
    wrapper.unmount()
  })

  it('should emit update:open false when clicking close button', async () => {
    const wrapper = mount(MobileBottomSheet, {
      props: { open: true, title: 'Test' },
      attachTo: document.body,
    })
    await nextTick()
    const closeBtn = document.querySelector('.mobile-sheet-close')
    expect(closeBtn).not.toBeNull()
    ;(closeBtn as HTMLElement).click()
    await nextTick()
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
    wrapper.unmount()
  })

  it('should close on Escape key', async () => {
    const wrapper = mount(MobileBottomSheet, {
      props: { open: true, title: 'Test' },
      attachTo: document.body,
    })
    await nextTick()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false])
    wrapper.unmount()
  })
})
