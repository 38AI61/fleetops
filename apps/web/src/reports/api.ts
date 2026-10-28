import { useQuery } from '@tanstack/react-query'
import { apiFetch, ApiError, getJson } from '../api/client'

// One driver's day as GET /reports/daily returns it (GF-324). on_time_pct is null
// when the driver had no delivery with a full window that day.
export interface ReportRow {
  driver_id: number
  driver_name: string
  jobs_completed: number
  on_time_count: number
  on_time_denom: number
  on_time_pct: number | null
  distance_m: number
  idle_s: number
}

export interface DailyReport {
  date: string
  rows: ReportRow[]
}

// dailyPath is the JSON route for one fleet-local day.
export function dailyPath(date: string): string {
  return `/reports/daily?date=${encodeURIComponent(date)}`
}

// csvPath is the CSV export for one day. It is the link's href; the click goes
// through downloadDailyCsv, because a plain link cannot send the bearer token.
export function csvPath(date: string): string {
  return `/reports/daily?date=${encodeURIComponent(date)}&format=csv`
}

// useDailyReport reads one day. A 403 is shown as a state, not retried.
export function useDailyReport(date: string) {
  return useQuery({
    queryKey: ['reports', date],
    queryFn: () => getJson<DailyReport>(dailyPath(date)),
    retry: false,
  })
}

// downloadDailyCsv fetches the export with the session token and saves it as daily-<date>.csv.
export async function downloadDailyCsv(date: string): Promise<void> {
  const res = await apiFetch(csvPath(date))
  if (!res.ok) throw new ApiError(res.status, 'download_failed')
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = `daily-${date}.csv`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
