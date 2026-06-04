declare module 'hast' {
  export type Element = {
    type: 'element'
    tagName: string
    properties?: Record<string, unknown>
    children: Array<
      | Element
      | {
          type: 'text'
          value: string
        }
    >
  }
}
