import { useQuery } from '@tanstack/react-query'
import { getJson } from '../api/client'
import type { Vehicle } from '../api/types'

// device_id → vehicle_id. Built from paired_device_id only (GF-163).
export function buildDeviceIndex(vehicles: Vehicle[]): Map<number, number> {
  const index = new Map<number, number>()
  for (const v of vehicles) {
    if (v.paired_device_id !== null) index.set(v.paired_device_id, v.id)
  }
  return index
}

// GET /vehicles is ops-only. The app refuses the driver role before this runs.
export function useVehicleIndex() {
  return useQuery({
    queryKey: ['vehicles'],
    queryFn: () => getJson<Vehicle[]>('/vehicles'),
    staleTime: 60_000,
    select: buildDeviceIndex,
  })
}
