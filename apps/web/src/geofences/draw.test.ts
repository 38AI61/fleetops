import { describe, expect, it } from 'vitest'
import {
  cancelDraw,
  circleRing,
  clickCircle,
  clickPolygon,
  draftFeature,
  draftCollection,
  finishPolygon,
  fencesCollection,
  isValidName,
  isValidRadius,
  polygonRing,
  startCircle,
  startPolygon,
  type LngLat,
} from './draw'

const A: LngLat = [13.0, 52.0]
const B: LngLat = [13.1, 52.0]
const C: LngLat = [13.1, 52.1]

// Great-circle distance on the same sphere the ring uses, in metres.
function distanceM(a: LngLat, b: LngLat): number {
  const rad = Math.PI / 180
  const dLat = (b[1] - a[1]) * rad
  const dLng = (b[0] - a[0]) * rad
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a[1] * rad) * Math.cos(b[1] * rad) * Math.sin(dLng / 2) ** 2
  return 2 * 6371008.8 * Math.asin(Math.sqrt(h))
}

describe('polygon path', () => {
  it('three clicks then a click on the first vertex gives a closed 4-position ring in lng,lat order', () => {
    let s = startPolygon()
    s = clickPolygon(s, A)
    s = clickPolygon(s, B)
    s = clickPolygon(s, C)
    expect(polygonRing(s)).toBeNull()
    s = clickPolygon(s, A)
    const ring = polygonRing(s)
    expect(ring).toEqual([A, B, C, A])
    expect(ring).toHaveLength(4)
    expect(ring?.[0]).toEqual(ring?.[3])
  })

  it('finish closes a polygon with at least three vertices', () => {
    let s = startPolygon()
    s = clickPolygon(s, A)
    s = clickPolygon(s, B)
    s = clickPolygon(s, C)
    s = finishPolygon(s)
    expect(polygonRing(s)).toEqual([A, B, C, A])
  })

  it('Escape cancels to idle', () => {
    let s = startPolygon()
    s = clickPolygon(s, A)
    s = clickPolygon(s, B)
    expect(cancelDraw()).toEqual({ mode: 'idle' })
    expect(s.mode).toBe('polygon')
  })

  it('fewer than three vertices cannot close', () => {
    let s = startPolygon()
    s = clickPolygon(s, A)
    s = clickPolygon(s, B)
    expect(finishPolygon(s)).toEqual(s)
    // A click on the first vertex with two vertices adds a vertex instead of closing.
    s = clickPolygon(s, A)
    expect(polygonRing(s)).toBeNull()
    expect(s).toMatchObject({ mode: 'polygon', closed: false })
  })
})

describe('circle path', () => {
  it('centre plus radius 500 gives a Point with properties.radius_m 500', () => {
    let s = startCircle()
    s = clickCircle(s, A)
    const f = draftFeature(s, 500, 'Depot ring')
    expect(f).toEqual({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: A },
      properties: { name: 'Depot ring', kind: 'circle', radius_m: 500 },
    })
  })

  it('later clicks do not move the centre', () => {
    let s = clickCircle(startCircle(), A)
    s = clickCircle(s, B)
    expect(s).toEqual({ mode: 'circle', centre: A })
  })
})

describe('circleRing', () => {
  it('is closed, has 65 positions, and every position is within 0.5% of the radius', () => {
    const ring = circleRing(A, 500)
    expect(ring).toHaveLength(65)
    expect(ring[0]).toEqual(ring[64])
    for (const p of ring) {
      expect(Math.abs(distanceM(A, p) - 500) / 500).toBeLessThan(0.005)
    }
  })
})

describe('client bounds mirror the server', () => {
  it('rejects radius 0 and 100001, and accepts the limits', () => {
    expect(isValidRadius(0)).toBe(false)
    expect(isValidRadius(100001)).toBe(false)
    expect(isValidRadius(1)).toBe(true)
    expect(isValidRadius(100000)).toBe(true)
    expect(isValidRadius(Number.NaN)).toBe(false)
  })

  it('trims names and bounds them to 1 to 100 runes', () => {
    expect(isValidName('   ')).toBe(false)
    expect(isValidName(' Depot ')).toBe(true)
    expect(isValidName('a'.repeat(100))).toBe(true)
    expect(isValidName('a'.repeat(101))).toBe(false)
  })

  it('draftFeature is null for an invalid radius or an incomplete shape', () => {
    const s = clickCircle(startCircle(), A)
    expect(draftFeature(s, 0, 'x')).toBeNull()
    expect(draftFeature(startCircle(), 500, 'x')).toBeNull()
  })
})

describe('overlay collections', () => {
  it('renders a stored circle as its centre point plus a display ring (GF-274)', () => {
    const col = fencesCollection([
      { id: 7, properties: { name: 'ring', kind: 'circle', radius_m: 500 }, geometry: { type: 'Point', coordinates: A } },
    ])
    expect(col.features.map((f) => f.geometry.type)).toEqual(['Point', 'LineString'])
  })

  it('draws the draft polygon as an open line until it closes', () => {
    let s = startPolygon()
    s = clickPolygon(s, A)
    s = clickPolygon(s, B)
    expect(draftCollection(s, 0).features.map((f) => f.geometry.type)).toEqual(['Point', 'Point', 'LineString'])
    s = clickPolygon(s, C)
    s = finishPolygon(s)
    expect(draftCollection(s, 0).features.map((f) => f.geometry.type)).toEqual(['Point', 'Point', 'Point', 'Polygon'])
  })
})
