import { useEffect, useMemo, useReducer, useState, useSyncExternalStore } from 'react'
import { Link } from 'react-router'
import { ApiError } from '../api/client'
import { getAuthState, subscribeAuth } from '../auth/session'
import { MapView } from '../map/MapView'
import { toFeatureCollection } from '../status/display'
import { useCreateFence, useDeleteFence, useFences } from './api'
import {
  cancelDraw,
  clickCircle,
  clickPolygon,
  draftCollection,
  draftFeature,
  fencesCollection,
  finishPolygon,
  idleDraw,
  isValidRadius,
  startCircle,
  startPolygon,
  toLngLat,
  type DrawState,
  type LngLat,
} from './draw'
import { AlertBell } from '../alerts/AlertBell'

// Draw actions as a reducer, the same shape NewJobPage uses for picks (GF-219).
type DrawAction =
  | { type: 'polygon' }
  | { type: 'circle' }
  | { type: 'cancel' }
  | { type: 'finish' }
  | { type: 'click'; point: LngLat }

function reduceDraw(state: DrawState, action: DrawAction): DrawState {
  switch (action.type) {
    case 'polygon':
      return startPolygon()
    case 'circle':
      return startCircle()
    case 'cancel':
      return cancelDraw()
    case 'finish':
      return finishPolygon(state)
    case 'click':
      return state.mode === 'circle' ? clickCircle(state, action.point) : clickPolygon(state, action.point)
  }
}

// Zones page (GF-273). Admin and dispatcher draw and delete (GF-268). Manager sees
// the list and the read-only fences on the map.
export function ZonesPage() {
  const role = useSyncExternalStore(subscribeAuth, getAuthState).session?.claims.role
  const canDraw = role === 'admin' || role === 'dispatcher'

  const fences = useFences()
  const create = useCreateFence()
  const remove = useDeleteFence()
  const [draw, dispatch] = useReducer(reduceDraw, idleDraw)
  const [name, setName] = useState('')
  const [radiusText, setRadiusText] = useState('500')
  const radius = Number(radiusText)

  // Escape cancels the draft from any mode (GF-272).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') dispatch({ type: 'cancel' })
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const features = useMemo(() => fences.data?.features ?? [], [fences.data])
  const fenceOverlay = useMemo(() => fencesCollection(features), [features])
  const draftOverlay = useMemo(() => draftCollection(draw, radius), [draw, radius])
  const emptyVehicles = useMemo(() => toFeatureCollection([], 0), [])
  const feature = draftFeature(draw, radius, name)

  function save() {
    if (!feature) return
    create.mutate(feature, {
      onSuccess: () => {
        dispatch({ type: 'cancel' })
        setName('')
      },
    })
  }

  return (
    <div className="zones-page">
      <header className="topbar">
        <span className="title">Zones</span>
        <Link to="/map">Map</Link>
        <Link to="/jobs">Jobs</Link>
        <AlertBell />
      </header>

      <div className="zones">
        <aside className="zones-side">
          {canDraw && (
            <section aria-label="Draw" className="zones-draw">
              <div role="group" aria-label="Draw controls">
                <button type="button" onClick={() => dispatch({ type: 'polygon' })}>
                  Draw polygon
                </button>
                <button type="button" onClick={() => dispatch({ type: 'circle' })}>
                  Draw circle
                </button>
                {draw.mode === 'polygon' && !draw.closed && (
                  <button type="button" onClick={() => dispatch({ type: 'finish' })}>
                    Finish
                  </button>
                )}
                {draw.mode !== 'idle' && (
                  <button type="button" onClick={() => dispatch({ type: 'cancel' })}>
                    Cancel
                  </button>
                )}
              </div>
              <p className="hint">{hint(draw, radius)}</p>
              <label>
                Name
                <input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} />
              </label>
              {draw.mode === 'circle' && (
                <label>
                  Radius (m)
                  <input
                    type="number"
                    min={1}
                    max={100000}
                    value={radiusText}
                    onChange={(e) => setRadiusText(e.target.value)}
                  />
                </label>
              )}
              <button type="button" disabled={!feature || create.isPending} onClick={save}>
                Save zone
              </button>
              {create.isError && <p role="alert">{saveMessage(create.error)}</p>}
            </section>
          )}

          <section aria-label="Zone list">
            {fences.isPending && <p>Loading zones</p>}
            {fences.isError && <p role="alert">Could not load zones</p>}
            <ul className="zones-list">
              {features.map((f) => (
                <li key={f.id}>
                  <span>{f.properties.name}</span>{' '}
                  <span>{f.properties.kind === 'circle' ? `circle ${f.properties.radius_m} m` : 'polygon'}</span>
                  {canDraw && (
                    <button type="button" onClick={() => remove.mutate(f.id)}>
                      Delete {f.properties.name}
                    </button>
                  )}
                </li>
              ))}
            </ul>
            {remove.isError && <p role="alert">Could not delete the zone</p>}
          </section>
        </aside>

        <div className="zones-map">
          <MapView
            collection={emptyVehicles}
            onBoundsChange={() => undefined}
            onSelect={() => undefined}
            onDeselect={() => undefined}
            onInteraction={() => undefined}
            followTarget={null}
            onMapClick={canDraw ? (p) => dispatch({ type: 'click', point: toLngLat(p) }) : undefined}
            fences={fenceOverlay}
            draft={draftOverlay}
          />
        </div>
      </div>
    </div>
  )
}

function hint(draw: DrawState, radius: number): string {
  switch (draw.mode) {
    case 'idle':
      return 'Choose a shape to draw.'
    case 'polygon':
      return draw.closed
        ? 'Polygon closed. Name it and save.'
        : 'Click the map to place vertices. Click the first vertex or press Finish to close.'
    case 'circle':
      if (!draw.centre) return 'Click the map to place the centre.'
      return isValidRadius(radius) ? 'Name it and save.' : 'Radius must be 1 to 100000 m.'
  }
}

function saveMessage(err: unknown): string {
  if (err instanceof ApiError && err.code === 'conflict') return 'A zone with this name already exists.'
  if (err instanceof ApiError && err.code === 'validation_failed') return 'The server refused this zone.'
  return 'Could not save the zone.'
}
