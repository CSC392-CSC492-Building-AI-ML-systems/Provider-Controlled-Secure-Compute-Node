import { useCallback, useEffect, useState } from 'react'

// usePoll calls load now and every intervalMs, keeping the latest result or error.
// refresh triggers an immediate reload.
export function usePoll<T>(load: () => Promise<T>, intervalMs: number) {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string>()

  const refresh = useCallback(() => {
    load().then(
      (d) => {
        setData(d)
        setError(undefined)
      },
      (e: unknown) => setError(e instanceof Error ? e.message : String(e)),
    )
  }, [load])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, intervalMs)
    return () => clearInterval(id)
  }, [refresh, intervalMs])

  return { data, error, refresh }
}
