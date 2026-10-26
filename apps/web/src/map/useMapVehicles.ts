import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { getJson } from '../api/client'
import type { MapVehiclesResult } from '../api/types'

// The bbox of the query on screen. The WS client writes into this key only.
let activeBbox: string | null = null

export function getActiveBbox(): string | null {
  return activeBbox
}

export function mapVehiclesKey(bbox: string) {
  return ['map-vehicles', bbox] as const
}

export function useMapVehicles(bbox: string) {
  useEffect(() => {
    activeBbox = bbox
  }, [bbox])
  return useQuery({
    queryKey: mapVehiclesKey(bbox),
    queryFn: () => getJson<MapVehiclesResult>(`/map/vehicles?bbox=${bbox}`),
    placeholderData: keepPreviousData,
    staleTime: 5_000,
    refetchInterval: 15_000,
  })
}
