import { useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { VehicleRow } from '../api/types'
import { logout } from '../auth/logout'
import { getAuthState } from '../auth/session'
import { VehicleCard } from '../details/VehicleCard'
import { toFeatureCollection, type VehicleCollection } from '../status/display'
import { ConnectionBadge } from '../ws/ConnectionBadge'
import { startLiveFeed } from '../ws/client'
import { MapView, type InteractionEvent } from './MapView'
import { bboxParam } from './bbox'
import { followCenter, nextFollowId, type FollowEvent } from './follow'
import { useMapVehicles } from './useMapVehicles'
import { useNow } from './useNow'

const WORLD_BBOX = bboxParam({ west: -180, south: -90, east: 180, north: 90 })
const NO_VEHICLES: never[] = []

export function MapPage() {
  const queryClient = useQueryClient()
  const [bbox, setBbox] = useState(WORLD_BBOX)
  const fleetId = getAuthState().session?.claims.fleet_id ?? null

  // One live feed per mounted map. Unmount (logout or route change) stops it.
  useEffect(() => {
    if (fleetId === null) return
    return startLiveFeed(queryClient, fleetId)
  }, [queryClient, fleetId])

  const query = useMapVehicles(bbox)
  const tick = Math.floor(useNow(1000) / 1000)
  const vehicles = query.data?.vehicles ?? NO_VEHICLES
  const truncated = query.data?.truncated ?? false

  const collection: VehicleCollection = useMemo(
    () => toFeatureCollection(vehicles, tick * 1000),
    [vehicles, tick],
  )

  // The card keeps the last snapshot while the selected vehicle is in view,
  // and after it leaves the data set (GF-159).
  const [selection, setSelection] = useState<{ id: number; snap: VehicleRow } | null>(null)
  const live = selection ? vehicles.find((v) => v.vehicle_id === selection.id) : undefined
  useEffect(() => {
    if (selection && live && live !== selection.snap) {
      setSelection({ id: selection.id, snap: live })
    }
  }, [selection, live])

  // Follow mode centers the camera on the selected vehicle (GF-161).
  const [followId, setFollowId] = useState<number | null>(null)
  const [followTarget, setFollowTarget] = useState<[number, number] | null>(null)
  const prevPosRef = useRef<{ lat: number; lng: number } | null>(null)
  useEffect(() => {
    if (followId === null) return
    const row = vehicles.find((v) => v.vehicle_id === followId)
    const target = followCenter(prevPosRef.current, row)
    if (row) prevPosRef.current = { lat: row.lat, lng: row.lng }
    if (target) setFollowTarget(target)
  }, [followId, vehicles])

  function applyFollow(event: FollowEvent) {
    setFollowId((current) => nextFollowId(current, event))
    if (event.type === 'select' || event.type === 'deselect' || event.type === 'stop') {
      prevPosRef.current = null
    }
  }

  function onSelect(vehicleId: number) {
    const row = vehicles.find((v) => v.vehicle_id === vehicleId)
    if (!row) return
    setSelection({ id: vehicleId, snap: row })
    applyFollow({ type: 'select', vehicleId })
  }

  function clearSelection() {
    setSelection(null)
    applyFollow({ type: 'deselect' })
  }

  function onInteraction(event: InteractionEvent) {
    applyFollow(event)
  }

  return (
    <div className="map-page">
      <header className="topbar">
        <span className="title">Operations map</span>
        <ConnectionBadge />
        <button type="button" onClick={() => void logout()}>
          Log out
        </button>
      </header>
      {truncated ? (
        <p className="banner" role="status">
          Showing the first {vehicles.length} vehicles. Zoom in to see all.
        </p>
      ) : null}
      {selection ? (
        <VehicleCard
          snapshot={selection.snap}
          nowMs={tick * 1000}
          inView={live !== undefined}
          following={followId === selection.id}
          onStopFollow={() => applyFollow({ type: 'stop' })}
          onClose={clearSelection}
        />
      ) : null}
      <MapView
        collection={collection}
        onBoundsChange={setBbox}
        onSelect={onSelect}
        onDeselect={clearSelection}
        onInteraction={onInteraction}
        followTarget={followTarget}
      />
    </div>
  )
}
