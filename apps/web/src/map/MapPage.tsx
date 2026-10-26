import { useEffect, useMemo, useState } from 'react'
import type { VehicleRow } from '../api/types'
import { logout } from '../auth/logout'
import { VehicleCard } from '../details/VehicleCard'
import { toFeatureCollection, type VehicleCollection } from '../status/display'
import { MapView } from './MapView'
import { bboxParam } from './bbox'
import { useMapVehicles } from './useMapVehicles'
import { useNow } from './useNow'

const WORLD_BBOX = bboxParam({ west: -180, south: -90, east: 180, north: 90 })
const NO_VEHICLES: never[] = []

export function MapPage() {
  const [bbox, setBbox] = useState(WORLD_BBOX)
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

  function onSelect(vehicleId: number) {
    const row = vehicles.find((v) => v.vehicle_id === vehicleId)
    if (row) setSelection({ id: vehicleId, snap: row })
  }

  return (
    <div className="map-page">
      <header className="topbar">
        <span className="title">Operations map</span>
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
          onClose={() => setSelection(null)}
        />
      ) : null}
      <MapView
        collection={collection}
        onBoundsChange={setBbox}
        onSelect={onSelect}
        onDeselect={() => setSelection(null)}
        onInteraction={() => undefined}
      />
    </div>
  )
}
