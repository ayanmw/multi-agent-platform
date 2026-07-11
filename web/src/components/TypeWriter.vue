<!-- TypeWriter — renders LLM streaming text with Markdown + syntax highlighting
     Props:
       text: the accumulated text from llm_delta events
       isStreaming: whether the LLM is still generating (true → show cursor)
       language: default code language for syntax highlighting (default: plaintext)

     Behavior:
       - Renders Markdown via marked, with highlight.js for code blocks
       - When isStreaming is true, shows a blinking cursor at the end
       - Auto-scrolls to the bottom when new text arrives
       - Sanitizes HTML (marked handles this by default)
       - Each code block gets a "Copy" button
       - JSON content in code blocks gets a "Format/Compact" toggle

     Design rationale:
       - Streaming text is the core of the "white-box" experience — the user sees
         every token the LLM generates in real time
       - Markdown rendering is done on the fly: each update re-renders the full text
         via marked. marked is fast enough for this (~1ms for typical agent output)
       - The blinking cursor is a visual cue that the LLM is still generating
       - Copy buttons on code blocks improve UX for generated code
-->
<script setup lang="ts">
import { computed, ref, watch, nextTick, onUnmounted } from 'vue'
import { Marked } from 'marked'
import { markedHighlight } from 'marked-highlight'
import hljs from 'highlight.js/lib/core'
// Import common languages for syntax highlighting
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import json from 'highlight.js/lib/languages/json'
import sql from 'highlight.js/lib/languages/sql'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'
import markdown from 'highlight.js/lib/languages/markdown'

// Register languages — only the ones we need to keep the bundle small
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('python', python)
hljs.registerLanguage('py', python)
hljs.registerLanguage('go', go)
hljs.registerLanguage('json', json)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('yml', yaml)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('md', markdown)

// Configure marked with highlight.js
// markedHighlight is a marked extension that integrates highlight.js
const marked = new Marked(
  markedHighlight({
    langPrefix: 'hljs language-',
    highlight(code: string, lang: string) {
      if (lang && hljs.getLanguage(lang)) {
        try {
          return hljs.highlight(code, { language: lang }).value
        } catch {
          // Fall through to auto-detect
        }
      }
      // Auto-detect language
      try {
        return hljs.highlightAuto(code).value
      } catch {
        return code
      }
    },
  })
)

const props = defineProps<{
  text: string
  isStreaming: boolean
}>()

const contentRef = ref<HTMLElement | null>(null)

/** Track which code blocks have had copy buttons injected */
let copyButtonsInjected = false

/**
 * F11 修复：injectCopyButtons 防抖定时器。
 * 每次 llm_delta 都会触发 text watch，若每次都 querySelectorAll + insertBefore
 * 在长输出下会高频操作 DOM 造成掉帧。这里用 100ms 防抖合并连续 delta。
 * 组件 unmount 时 clearTimeout 防止泄漏。
 */
let injectTimer: ReturnType<typeof setTimeout> | null = null

/** Render Markdown to HTML. Returns empty string for empty input. */
const renderedHTML = computed(() => {
  if (!props.text) return ''
  copyButtonsInjected = false
  // marked.parse returns string | Promise<string>; for sync usage it's always string
  return marked.parse(props.text, { breaks: true, gfm: true }) as string
})

/** Inject copy buttons into code blocks after DOM update */
async function injectCopyButtons() {
  if (copyButtonsInjected) return
  await nextTick()
  if (!contentRef.value) return

  const pres = contentRef.value.querySelectorAll('pre')
  for (const pre of pres) {
    // Skip if already has a copy button wrapper
    if (pre.querySelector('.code-toolbar')) continue

    const code = pre.querySelector('code')
    const codeText = code?.textContent || ''

    // Wrap pre in a toolbar container
    const wrapper = document.createElement('div')
    wrapper.className = 'code-toolbar'
    pre.parentNode?.insertBefore(wrapper, pre)
    wrapper.appendChild(pre)

    // Create toolbar
    const toolbar = document.createElement('div')
    toolbar.className = 'code-toolbar-actions'

    // Detect if this is JSON content for format toggle
    const lang = code?.className.match(/language-(\w+)/)?.[1] || ''
    const isJson = lang === 'json' || (lang === '' && isJsonLike(codeText))

    if (isJson && codeText) {
      // Format/Compact toggle button
      const formatBtn = document.createElement('button')
      formatBtn.className = 'code-action-btn'
      formatBtn.textContent = 'Format'
      formatBtn.title = 'Toggle JSON formatting'
      let formatted = false
      formatBtn.addEventListener('click', () => {
        try {
          const parsed = JSON.parse(codeText)
          if (formatted) {
            code!.textContent = JSON.stringify(parsed)
            formatBtn.textContent = 'Format'
          } else {
            code!.textContent = JSON.stringify(parsed, null, 2)
            formatBtn.textContent = 'Compact'
          }
          formatted = !formatted
          // Re-highlight
          if (code) {
            hljs.highlightElement(code)
          }
        } catch {
          // Not valid JSON, ignore
        }
      })
      toolbar.appendChild(formatBtn)
    }

    // Copy button
    const copyBtn = document.createElement('button')
    copyBtn.className = 'code-action-btn'
    copyBtn.textContent = 'Copy'
    copyBtn.title = 'Copy to clipboard'
    copyBtn.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(codeText)
        copyBtn.textContent = 'Copied!'
        copyBtn.classList.add('copied')
        setTimeout(() => {
          copyBtn.textContent = 'Copy'
          copyBtn.classList.remove('copied')
        }, 2000)
      } catch {
        copyBtn.textContent = 'Failed'
        setTimeout(() => {
          copyBtn.textContent = 'Copy'
        }, 2000)
      }
    })
    toolbar.appendChild(copyBtn)

    wrapper.insertBefore(toolbar, pre)
  }

  copyButtonsInjected = true
}

/** Check if text looks like JSON (starts with { or [) */
function isJsonLike(text: string): boolean {
  const trimmed = text.trim()
  return (trimmed.startsWith('{') || trimmed.startsWith('['))
}

/** Auto-scroll to the bottom when new content arrives.
 *  F11: injectCopyButtons 走 100ms 防抖，避免每个 delta 都重 DOM。 */
watch(
  () => props.text,
  async () => {
    await nextTick()
    if (contentRef.value) {
      contentRef.value.scrollTop = contentRef.value.scrollHeight
    }
    // Inject copy buttons after each render — debounced to avoid per-delta DOM thrash
    if (injectTimer) clearTimeout(injectTimer)
    injectTimer = setTimeout(() => {
      injectCopyButtons()
    }, 100)
  }
)

/** F11: 组件卸载时清理防抖定时器，防止在已卸载的 DOM 上执行 insertBefore */
onUnmounted(() => {
  if (injectTimer) {
    clearTimeout(injectTimer)
    injectTimer = null
  }
})
</script>

<template>
  <div class="typewriter">
    <div
      ref="contentRef"
      class="typewriter-content markdown-body"
      v-html="renderedHTML"
    ></div>
    <!-- Blinking cursor — indicates the LLM is still generating -->
    <span v-if="isStreaming" class="typewriter-cursor">▌</span>
  </div>
</template>

<style scoped>
.typewriter {
  position: relative;
}

.typewriter-content {
  white-space: pre-wrap;
  word-break: break-word;
  line-height: 1.6;
}

/* Blinking cursor animation */
.typewriter-cursor {
  display: inline;
  color: #4a9eff;
  animation: blink 1s step-end infinite;
  font-size: 14px;
  vertical-align: text-bottom;
}

@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}

/* ============================================================
   Markdown rendering styles (GitHub-like dark theme)
   ============================================================ */
.markdown-body {
  color: #d4d4d4;
  font-size: 13px;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4) {
  color: #e0e0e0;
  margin: 12px 0 6px;
  font-weight: 600;
}

.markdown-body :deep(h1) { font-size: 18px; border-bottom: 1px solid #444; padding-bottom: 4px; }
.markdown-body :deep(h2) { font-size: 16px; }
.markdown-body :deep(h3) { font-size: 14px; }

.markdown-body :deep(p) {
  margin: 4px 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 20px;
  margin: 4px 0;
}

.markdown-body :deep(li) {
  margin: 2px 0;
}

.markdown-body :deep(code) {
  background: #333;
  padding: 1px 5px;
  border-radius: 3px;
  font-family: 'Consolas', 'Monaco', 'Courier New', monospace;
  font-size: 12px;
  color: #ce9178;
}

.markdown-body :deep(pre) {
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 6px;
  padding: 12px;
  overflow-x: auto;
  margin: 8px 0;
}

.markdown-body :deep(pre code) {
  background: none;
  padding: 0;
  color: inherit;
  font-size: 12px;
  line-height: 1.5;
}

.markdown-body :deep(blockquote) {
  border-left: 3px solid #4a9eff;
  margin: 8px 0;
  padding: 4px 12px;
  color: #999;
  background: #2a2a2a;
  border-radius: 0 4px 4px 0;
}

.markdown-body :deep(table) {
  border-collapse: collapse;
  margin: 8px 0;
  width: 100%;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid #444;
  padding: 6px 10px;
  text-align: left;
  font-size: 12px;
}

.markdown-body :deep(th) {
  background: #333;
  font-weight: 600;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid #444;
  margin: 12px 0;
}

.markdown-body :deep(a) {
  color: #4a9eff;
  text-decoration: none;
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(strong) {
  color: #e0e0e0;
}

.markdown-body :deep(img) {
  max-width: 100%;
  border-radius: 4px;
}

/* Code toolbar — copy button + format/compact toggle */
.markdown-body :deep(.code-toolbar) {
  position: relative;
}

.markdown-body :deep(.code-toolbar-actions) {
  position: absolute;
  top: 8px;
  right: 8px;
  display: flex;
  gap: 6px;
  opacity: 0;
  transition: opacity 0.2s;
}

.markdown-body :deep(.code-toolbar:hover .code-toolbar-actions) {
  opacity: 1;
}

.markdown-body :deep(.code-action-btn) {
  background: #333;
  color: #ccc;
  border: 1px solid #555;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 10px;
  font-family: inherit;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.markdown-body :deep(.code-action-btn:hover) {
  background: #4a9eff;
  color: #fff;
  border-color: #4a9eff;
}

.markdown-body :deep(.code-action-btn.copied) {
  background: #51cf66;
  color: #fff;
  border-color: #51cf66;
}
</style>