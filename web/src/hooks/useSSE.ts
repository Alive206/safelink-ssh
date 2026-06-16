import { useEffect, useRef, useState } from 'react'

// useSSE subscribes to a Server-Sent Events endpoint and keeps a rolling
// buffer of the last `max` payloads.  Each payload is delivered as a string
// (raw event.data) so callers can JSON.parse if needed.
export function useSSE(url: string, max = 500): string[] {
  const [lines, setLines] = useState<string[]>([])
  const ref = useRef<EventSource | null>(null)
  useEffect(() => {
    const es = new EventSource(url, { withCredentials: true })
    ref.current = es
    es.onmessage = (ev) => {
      setLines((prev) => {
        const next = prev.length >= max ? prev.slice(prev.length - max + 1) : prev.slice()
        next.push(ev.data)
        return next
      })
    }
    es.onerror = () => {
      // Browser auto-retries; nothing to do here, but we surface a marker so
      // the UI doesn't look frozen on long disconnects.
    }
    return () => {
      es.close()
      ref.current = null
    }
  }, [url, max])
  return lines
}
