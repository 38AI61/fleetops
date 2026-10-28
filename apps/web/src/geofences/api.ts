import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, apiFetch, getJson, sendJson } from '../api/client'
import type { FenceFeature, LngLat } from './draw'

// Wire shape of a stored fence (GF-263). The server adds id and bbox.
export interface StoredFenceFeature {
  type: 'Feature'
  id: number
  bbox: number[]
  geometry: { type: 'Polygon'; coordinates: LngLat[][] } | { type: 'Point'; coordinates: LngLat }
  properties: { name: string; kind: string; radius_m?: number; dwell_min?: number; schedule?: unknown }
}

export interface FenceCollection {
  type: 'FeatureCollection'
  features: StoredFenceFeature[]
}

export const fencesKey = ['geofences'] as const

export function listFences(): Promise<FenceCollection> {
  return getJson<FenceCollection>('/geofences')
}

export function createFence(body: FenceFeature): Promise<StoredFenceFeature> {
  return sendJson<StoredFenceFeature>('POST', '/geofences', body)
}

// DELETE answers 204 with no body, so it cannot go through sendJson, which parses JSON.
export async function deleteFence(id: number): Promise<void> {
  const res = await apiFetch(`/geofences/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new ApiError(res.status, await errorCode(res))
}

async function errorCode(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: unknown }
    return typeof body.error === 'string' ? body.error : 'http_error'
  } catch {
    return 'http_error'
  }
}

export function useFences() {
  return useQuery({ queryKey: fencesKey, queryFn: listFences })
}

export function useCreateFence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: FenceFeature) => createFence(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: fencesKey }),
  })
}

export function useDeleteFence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => deleteFence(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: fencesKey }),
  })
}
