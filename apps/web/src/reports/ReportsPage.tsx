import { useState, type MouseEvent } from 'react'
import { Link } from 'react-router'
import { AlertBell } from '../alerts/AlertBell'
import { ApiError } from '../api/client'
import { csvPath, downloadDailyCsv, useDailyReport, type DailyReport, type ReportRow } from './api'

// todayLocal is the browser's calendar date as YYYY-MM-DD, the default day.
function todayLocal(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

// ReportsPage shows one fleet-local day per driver (P21). The server applies the
// role scope: a driver sees only their own row (GF-324).
export function ReportsPage() {
  const [date, setDate] = useState(todayLocal)
  const [downloadError, setDownloadError] = useState<string | null>(null)
  const report = useDailyReport(date)

  function onDownload(e: MouseEvent<HTMLAnchorElement>) {
    e.preventDefault()
    setDownloadError(null)
    downloadDailyCsv(date).catch(() => setDownloadError('The CSV could not be downloaded. Try again.'))
  }

  return (
    <div className="reports-page">
      <header className="topbar">
        <span className="title">Reports</span>
        <Link to="/map">Map</Link>
        <Link to="/jobs">Jobs</Link>
        <Link to="/zones">Zones</Link>
        <AlertBell />
      </header>

      <main className="reports">
        <div className="reports-controls">
          <label>
            Date <input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          </label>
          <a href={csvPath(date)} onClick={onDownload}>
            Download CSV
          </a>
        </div>
        {downloadError && <p role="alert">{downloadError}</p>}
        <ReportBody report={report} />
      </main>
    </div>
  )
}

// ReportBody renders the four query states: loading, forbidden, error and data.
function ReportBody({ report }: { report: ReturnType<typeof useDailyReport> }) {
  if (report.isError) {
    if (report.error instanceof ApiError && report.error.status === 403) {
      return <p role="alert">This report is not available for your role.</p>
    }
    return <p role="alert">The report could not be loaded. Try again.</p>
  }
  if (!report.isSuccess) return <p>Loading</p>
  return <ReportTable rows={(report.data as DailyReport).rows} />
}

function ReportTable({ rows }: { rows: ReportRow[] }) {
  if (rows.length === 0) return <p>No driving or deliveries were recorded for this day.</p>
  return (
    <table>
      <thead>
        <tr>
          <th scope="col">Driver</th>
          <th scope="col">Jobs</th>
          <th scope="col">On time</th>
          <th scope="col">Distance (km)</th>
          <th scope="col">Idle (min)</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.driver_id}>
            <td>{r.driver_name}</td>
            <td>{r.jobs_completed}</td>
            <td>{onTimeText(r)}</td>
            <td>{(r.distance_m / 1000).toFixed(1)}</td>
            <td>{Math.round(r.idle_s / 60)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// onTimeText shows the counts and the rounded percentage, or a dash when there is no denominator.
function onTimeText(r: ReportRow): string {
  if (r.on_time_pct === null) return '—'
  return `${r.on_time_count} of ${r.on_time_denom} (${r.on_time_pct}%)`
}
