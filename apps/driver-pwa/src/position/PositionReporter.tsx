import { useEffect, useRef, useState } from 'react'
import { postPosition } from '../api/ingest'
import { clearDeviceKey } from '../device/key'
import { readPosition } from './geo'
import { fixToIngestBody } from './payload'
import { useOnShift } from './shift'

// One position per tick while on shift (GF-232). P14 does no background tracking.
const TICK_MS = 10_000

interface Props {
  deviceKey: string
  // Called after a 401 clears the stored key. The parent routes to key setup.
  onKeyRejected: () => void
}

// Mounted only with a stored key (RequireDeviceKey). Renders a status line, nothing else.
export function PositionReporter({ deviceKey, onKeyRejected }: Props) {
  const onShift = useOnShift()
  const [status, setStatus] = useState<string | null>(null)

  // The interval reads the latest props, so a replaced key is used on the next tick.
  const latest = useRef({ deviceKey, onKeyRejected })
  useEffect(() => {
    latest.current = { deviceKey, onKeyRejected }
  })

  useEffect(() => {
    if (!onShift) return

    let timer: ReturnType<typeof setInterval> | undefined
    let stopped = false

    async function tick() {
      if (stopped || document.visibilityState !== 'visible') return
      let fix: GeolocationPosition
      try {
        fix = await readPosition({ enableHighAccuracy: true, maximumAge: 5000, timeout: 8000 })
      } catch {
        setStatus('Location unavailable')
        return
      }
      let res: Response
      try {
        res = await postPosition(latest.current.deviceKey, fixToIngestBody(fix.coords, fix.timestamp))
      } catch {
        return // network error: dropped, the next tick retries
      }
      if (stopped) return
      if (res.status === 204) {
        setStatus(null)
      } else if (res.status === 401) {
        // GF-228: the key is rejected. Clear it, stop, and let the parent re-route.
        stopped = true
        clearInterval(timer)
        clearDeviceKey()
        latest.current.onKeyRejected()
      } else if (res.status === 400) {
        setStatus('Position rejected') // a bug signal, not retried
      }
      // 503 busy and other statuses: dropped silently, the next tick retries.
    }

    timer = setInterval(() => {
      void tick()
    }, TICK_MS)
    return () => {
      stopped = true
      clearInterval(timer)
    }
  }, [onShift])

  return status ? <p role="status">{status}</p> : null
}
