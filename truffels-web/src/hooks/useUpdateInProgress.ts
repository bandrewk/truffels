import { useEffect, useRef, useState } from 'react'

// Polls /api/truffels/updates/logs every 5 s. Returns true when:
// - any log entry indicates truffels is in `restarting` phase, OR
// - 2+ consecutive polls fail AND a recent successful poll was seen <10 min ago
// (transient network errors during the actual self-update restart window).
export function useUpdateInProgress(): boolean {
  const [active, setActive] = useState(false)
  const consecFailRef = useRef(0)
  const lastSuccessRef = useRef<number | null>(null)

  useEffect(() => {
    let cancelled = false
    const tick = async () => {
      try {
        const res = await fetch('/api/truffels/updates/logs', { credentials: 'same-origin' })
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const body = await res.json()
        const logs: Array<{ service_id: string; status: string }> = Array.isArray(body) ? body : (body.logs ?? [])
        const truffelsRestarting = logs.some(
          (l) => l.service_id === 'truffels' && l.status === 'restarting',
        )
        if (cancelled) return
        consecFailRef.current = 0
        lastSuccessRef.current = Date.now()
        setActive(truffelsRestarting)
      } catch {
        if (cancelled) return
        consecFailRef.current += 1
        const lastSuccess = lastSuccessRef.current
        if (
          consecFailRef.current >= 2 &&
          lastSuccess !== null &&
          Date.now() - lastSuccess < 10 * 60 * 1000
        ) {
          setActive(true)
        }
      }
    }
    void tick()
    const id = setInterval(tick, 5000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  return active
}
