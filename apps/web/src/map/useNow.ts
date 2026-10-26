import { useEffect, useState } from 'react'

// Re-renders every intervalMs so a silent vehicle grays without any data (GF-160).
export function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
