/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

declare module 'marked-highlight' {
  import type { MarkedExtension } from 'marked'
  export function markedHighlight(options: {
    langPrefix?: string
    highlight?: (code: string, lang: string, callback?: (error: Error | null, code: string) => void) => string | void
  }): MarkedExtension
}
