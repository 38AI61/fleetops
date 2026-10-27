import 'maplibre-gl/dist/maplibre-gl.css'
import { useEffect, useRef } from 'react'
import { Map as MapLibreMap, type GeoJSONSource, type StyleSpecification } from 'maplibre-gl'
import type { LatLng } from '../api/types'
import { marksCollection, type PickMarks } from '../jobs/pick'
import type { VehicleCollection } from '../status/display'
import { bboxParam } from './bbox'

// User and camera interactions the follow logic reads (GF-161). originalEvent is
// set only for user-originated gestures.
export interface InteractionEvent {
  type: 'dragstart' | 'zoomstart'
  originalEvent?: unknown
}

export interface MapViewProps {
  collection: VehicleCollection
  onBoundsChange: (bbox: string) => void
  onSelect: (vehicleId: number) => void
  onDeselect: () => void
  onInteraction: (event: InteractionEvent) => void
  // Camera center for follow mode. Zoom is left alone (GF-161).
  followTarget: [number, number] | null
  // Pick mode (GF-219). When onMapClick is set, an empty-space click calls it instead of onDeselect.
  onMapClick?: (point: LatLng) => void
  marks?: PickMarks
}

const SOURCE_ID = 'vehicles'
const PICKS_SOURCE_ID = 'picks'
const BOUNDS_DEBOUNCE_MS = 400

// Background only. P9 renders no basemap tiles, so the style works offline (GF-158).
const BLANK_STYLE: StyleSpecification = {
  version: 8,
  sources: {},
  layers: [{ id: 'background', type: 'background', paint: { 'background-color': '#e5e7eb' } }],
}

// Thin wiring only. Decisions live in the pure modules (GF-167).
export function MapView(props: MapViewProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<MapLibreMap | null>(null)
  const loadedRef = useRef(false)
  const propsRef = useRef(props)

  useEffect(() => {
    propsRef.current = props
  })

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const map = new MapLibreMap({ container, style: BLANK_STYLE, center: [13.405, 52.52], zoom: 11 })
    mapRef.current = map
    let boundsTimer: ReturnType<typeof setTimeout> | undefined

    const scheduleBounds = () => {
      clearTimeout(boundsTimer)
      boundsTimer = setTimeout(() => {
        const b = map.getBounds()
        propsRef.current.onBoundsChange(
          bboxParam({ west: b.getWest(), south: b.getSouth(), east: b.getEast(), north: b.getNorth() }),
        )
      }, BOUNDS_DEBOUNCE_MS)
    }

    map.on('load', () => {
      map.addSource(SOURCE_ID, {
        type: 'geojson',
        data: propsRef.current.collection,
        cluster: true,
        clusterRadius: 50,
        clusterMaxZoom: 14,
        clusterMinPoints: 2,
      })
      map.addLayer({
        id: 'clusters',
        type: 'circle',
        source: SOURCE_ID,
        filter: ['has', 'point_count'],
        paint: {
          'circle-color': '#1e3a8a',
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 1.5,
          'circle-radius': ['step', ['get', 'point_count'], 14, 10, 18, 50, 24],
        },
      })
      map.addLayer({
        id: 'unclustered',
        type: 'circle',
        source: SOURCE_ID,
        filter: ['!', ['has', 'point_count']],
        paint: {
          'circle-radius': 7,
          'circle-color': ['get', 'color'],
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 1.5,
          'circle-opacity': ['case', ['get', 'grayed'], 0.45, 1],
        },
      })
      map.addSource(PICKS_SOURCE_ID, { type: 'geojson', data: marksCollection(propsRef.current.marks ?? {}) })
      map.addLayer({
        id: 'picks',
        type: 'circle',
        source: PICKS_SOURCE_ID,
        paint: {
          'circle-radius': 6,
          'circle-color': ['match', ['get', 'kind'], 'pickup', '#16a34a', '#dc2626'],
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 2,
        },
      })
      loadedRef.current = true
      scheduleBounds()
    })

    map.on('moveend', scheduleBounds)
    map.on('dragstart', (e) => propsRef.current.onInteraction({ type: 'dragstart', originalEvent: e.originalEvent }))
    map.on('zoomstart', (e) => propsRef.current.onInteraction({ type: 'zoomstart', originalEvent: e.originalEvent }))

    map.on('click', (e) => {
      const [hit] = map.queryRenderedFeatures(e.point, { layers: ['clusters', 'unclustered'] })
      if (!hit) {
        if (propsRef.current.onMapClick) propsRef.current.onMapClick({ lat: e.lngLat.lat, lng: e.lngLat.lng })
        else propsRef.current.onDeselect()
        return
      }
      if (hit.layer.id === 'clusters') {
        const clusterId = Number(hit.properties.cluster_id)
        const source = map.getSource<GeoJSONSource>(SOURCE_ID)
        if (!source) return
        void source.getClusterExpansionZoom(clusterId).then((zoom) => {
          if (hit.geometry.type !== 'Point') return
          map.easeTo({ center: [hit.geometry.coordinates[0] ?? 0, hit.geometry.coordinates[1] ?? 0], zoom })
        })
        return
      }
      propsRef.current.onSelect(Number(hit.properties.vehicle_id))
    })

    return () => {
      clearTimeout(boundsTimer)
      loadedRef.current = false
      mapRef.current = null
      map.remove()
    }
  }, [])

  useEffect(() => {
    const map = mapRef.current
    if (!map || !loadedRef.current) return
    map.getSource<GeoJSONSource>(SOURCE_ID)?.setData(props.collection)
  }, [props.collection])

  useEffect(() => {
    const map = mapRef.current
    if (!map || !loadedRef.current) return
    map.getSource<GeoJSONSource>(PICKS_SOURCE_ID)?.setData(marksCollection(props.marks ?? {}))
    // Keyed on the points, not the marks object, so a parent re-render does not re-set the data.
  }, [props.marks?.pickup, props.marks?.dropoff])

  useEffect(() => {
    const map = mapRef.current
    if (!map || !props.followTarget) return
    map.easeTo({ center: props.followTarget, duration: 500 })
  }, [props.followTarget])

  return <div ref={containerRef} className="map-canvas" />
}
