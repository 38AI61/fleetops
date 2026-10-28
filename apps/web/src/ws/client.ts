import type { QueryClient } from '@tanstack/react-query'
import type { MapVehiclesResult, Vehicle } from '../api/types'
import { logout } from '../auth/logout'
import { singleFlightRefresh } from '../auth/refresh'
import { currentAccessToken, getAuthState, subscribeAuth } from '../auth/session'
import { getActiveBbox, mapVehiclesKey } from '../map/useMapVehicles'
import { buildDeviceIndex } from '../map/useVehicleIndex'
import { backoffDelay, initialWsState, reduceWsEvent, type WsEffect, type WsState } from './reducer'
import { setWsStatus } from './status'

// Client-side auth window, matching the hub's AuthWindow (GF-110).
const AUTH_TIMEOUT_MS = 5_000
// Rotate the socket this long before the access token expires (GF-162).
const ROTATE_MARGIN_MS = 60_000
const REFETCH_DEBOUNCE_MS = 1_000

interface Conn {
  ws: WebSocket
  authTimer?: ReturnType<typeof setTimeout>
  // True when this client closed the socket on purpose (superseded or stopped).
  ownClose: boolean
}

// Wire URL is derived from location, so the browser never hard-codes 10011 (GF-153).
// VITE_WS_BASE (cloud build, GF-352) sends the socket straight to the api host, for
// when the Pages /stream proxy cannot carry the upgrade. Unset keeps the same-origin URL.
export function wsUrl(fleetId: number, loc: Location = window.location): string {
  const base = import.meta.env.VITE_WS_BASE
  if (base) {
    return `${base.replace(/\/+$/, '')}/stream?fleet=${fleetId}`
  }
  const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${loc.host}/stream?fleet=${fleetId}`
}

function messageType(raw: string): string | undefined {
  try {
    const value = JSON.parse(raw) as { type?: unknown }
    return typeof value.type === 'string' ? value.type : undefined
  } catch {
    return undefined
  }
}

// Returns a stop function. One socket per authenticated session (GF-162).
export function startLiveFeed(queryClient: QueryClient, fleetId: number): () => void {
  let stopped = false
  let state: WsState = initialWsState(fleetId)
  let current: Conn | null = null
  let attempt = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined
  let rotateTimer: ReturnType<typeof setTimeout> | undefined
  let mapTimer: ReturnType<typeof setTimeout> | undefined
  let vehiclesTimer: ReturnType<typeof setTimeout> | undefined
  let indexSource: Vehicle[] | undefined
  let index = new Map<number, number>()

  function deviceIndex() {
    const raw = queryClient.getQueryData<Vehicle[]>(['vehicles'])
    if (raw !== indexSource) {
      indexSource = raw
      index = buildDeviceIndex(raw ?? [])
    }
    return { deviceToVehicle: index }
  }

  function schedule(effect: WsEffect) {
    if (effect === 'refetch-map') {
      clearTimeout(mapTimer)
      mapTimer = setTimeout(() => {
        void queryClient.invalidateQueries({ queryKey: ['map-vehicles'] })
      }, REFETCH_DEBOUNCE_MS)
    } else {
      clearTimeout(vehiclesTimer)
      vehiclesTimer = setTimeout(() => {
        void queryClient.invalidateQueries({ queryKey: ['vehicles'] })
      }, REFETCH_DEBOUNCE_MS)
    }
  }

  function promote(conn: Conn) {
    clearTimeout(conn.authTimer)
    attempt = 0
    setWsStatus('live')
    if (current && current !== conn) {
      current.ownClose = true
      current.ws.close(1000, 'rotated')
    }
    current = conn
  }

  function handleMessage(conn: Conn, raw: string) {
    if (messageType(raw) === 'auth.ok' && conn !== current) promote(conn)
    if (conn !== current) return
    const bbox = getActiveBbox()
    const key = bbox ? mapVehiclesKey(bbox) : null
    const data = key ? queryClient.getQueryData<MapVehiclesResult>(key) : undefined
    const result = reduceWsEvent(state, raw, deviceIndex(), data?.vehicles ?? [])
    state = result.state
    if (result.rows && key && data) {
      queryClient.setQueryData<MapVehiclesResult>(key, { ...data, vehicles: result.rows })
    }
    for (const effect of result.effects) schedule(effect)
  }

  function fatal() {
    stop()
    void logout()
  }

  function scheduleReconnect() {
    if (stopped) return
    clearTimeout(reconnectTimer)
    const delay = backoffDelay(attempt, Math.random)
    attempt += 1
    reconnectTimer = setTimeout(connect, delay)
  }

  function connect() {
    if (stopped) return
    const token = currentAccessToken()
    if (!token) return
    const ws = new WebSocket(wsUrl(fleetId))
    const conn: Conn = { ws, ownClose: false }

    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'auth', token }))
      conn.authTimer = setTimeout(() => {
        // No auth.ok in time counts as a failure (GF-162).
        ws.close(1000, 'auth timeout')
        // A failed rotation leaves the healthy socket in place. Only a missing
        // socket needs a reconnect.
        if (!current) scheduleReconnect()
      }, AUTH_TIMEOUT_MS)
    }

    ws.onmessage = (event) => {
      if (typeof event.data === 'string') handleMessage(conn, event.data)
    }

    ws.onclose = (event) => {
      clearTimeout(conn.authTimer)
      if (stopped || conn.ownClose) return
      // A socket that never became current is a failed rotation. The old socket
      // keeps running until its own close (GF-162), so nothing else to do here.
      if (conn !== current) return
      current = null
      setWsStatus('reconnecting')
      if (event.code === 4403) {
        fatal()
        return
      }
      if (event.code === 4401) {
        void singleFlightRefresh().then((ok) => {
          if (ok) scheduleReconnect()
          else fatal()
        })
        return
      }
      scheduleReconnect()
    }
  }

  // Proactive rotation: open and auth a new socket before the token expires,
  // then close the old one on the new auth.ok (GF-162). A fresh token comes first.
  function armRotate() {
    clearTimeout(rotateTimer)
    const session = getAuthState().session
    if (!session || stopped) return
    const delay = Math.max(0, session.expiresAt - ROTATE_MARGIN_MS - Date.now())
    rotateTimer = setTimeout(() => {
      void singleFlightRefresh().then(() => {
        if (!stopped && current) connect()
      })
    }, delay)
  }

  const unsubscribe = subscribeAuth(armRotate)
  setWsStatus('reconnecting')
  armRotate()
  connect()

  function stop() {
    if (stopped) return
    stopped = true
    unsubscribe()
    clearTimeout(reconnectTimer)
    clearTimeout(rotateTimer)
    clearTimeout(mapTimer)
    clearTimeout(vehiclesTimer)
    if (current) {
      current.ownClose = true
      current.ws.close(1000, 'client stop')
      current = null
    }
    setWsStatus('offline')
  }

  return stop
}
