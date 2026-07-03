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

     Design rationale:
       - Streaming text is the core of the "white-box" experience — the user sees
         every token the LLM generates in real time
       - Markdown rendering is done on the fly: each update re-renders the full text
         via marked. marked is fast enough for this (~1ms for typical agent output)
       - The blinking cursor is a visual cue that the LLM is still generating
-->
<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
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

/** Render Markdown to HTML. Returns empty string for empty input. */
const renderedHTML = computed(() => {
  if (!props.text) return ''
  // marked.parse returns string | Promise<string>; for sync usage it's always string
  return marked.parse(props.text, { breaks: true, gfm: true }) as string
})

/** Auto-scroll to the bottom when new content arrives */
watch(
  () => props.text,
  async () => {
    await nextTick()
    if (contentRef.value) {
      contentRef.value.scrollTop = contentRef.value.scrollHeight
    }
  }
)
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
</style>