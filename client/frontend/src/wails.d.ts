export {}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          GetVersion(): Promise<string>
          ListTunnels(): Promise<any[]>
        }
      }
    }
  }
}
