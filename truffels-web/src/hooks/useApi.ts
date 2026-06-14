import { useEffect, useState, useCallback } from 'react'

export function useApi<T>(fetcher: () => Promise<T>, intervalMs = 0) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [consecutiveErrors, setConsecutiveErrors] = useState(0)

  const refresh = useCallback(() => {
    fetcher()
      .then((d) => { setData(d); setError(null); setConsecutiveErrors(0) })
      .catch((e) => { setError(e.message); setConsecutiveErrors((n) => n + 1) })
      .finally(() => setLoading(false))
  }, [fetcher])

  useEffect(() => {
    refresh()
    if (intervalMs > 0) {
      const id = setInterval(refresh, intervalMs)
      return () => clearInterval(id)
    }
  }, [refresh, intervalMs])

  return { data, error, loading, refresh, consecutiveErrors }
}
