import { useMemo, useState } from 'react'
import { logout } from '../auth/logout'
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
      <MapView
        collection={collection}
        onBoundsChange={setBbox}
        onSelect={() => undefined}
        onDeselect={() => undefined}
        onInteraction={() => undefined}
      />
    </div>
  )
}
