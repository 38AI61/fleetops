import { useQueryClient } from '@tanstack/react-query'
import { TODAY_QUERY_KEY } from '../jobs/keys'
import { ACTION_LABELS } from '../jobs/actions'
import type { ActionRow } from './db'
import { discardJob, retry } from './queue'
import { runReplayPass } from './replay'

interface ConflictListProps {
  userId: string
  conflicts: ActionRow[]
}

// Conflicts stay until the driver acts on them (GF-240). Nothing is removed on its own.
export function ConflictList({ userId, conflicts }: ConflictListProps) {
  const client = useQueryClient()
  if (conflicts.length === 0) return null

  async function onRetry(row: ActionRow) {
    if (row.seq === undefined) return
    await retry(row.seq)
    await runReplayPass(userId, client)
  }

  async function onDiscard(row: ActionRow) {
    await discardJob(userId, row.jobId)
    await client.invalidateQueries({ queryKey: TODAY_QUERY_KEY })
  }

  return (
    <section aria-label="Needs attention">
      <h2>{conflicts.length} need attention</h2>
      <ul>
        {conflicts.map((row) => (
          <li key={row.seq}>
            <strong>Job {row.jobId}</strong> <span>{ACTION_LABELS[row.action]}</span>
            <p>{row.conflict?.message}</p>
            <button type="button" onClick={() => void onRetry(row)}>
              Retry
            </button>{' '}
            <button type="button" onClick={() => void onDiscard(row)}>
              Discard
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}
