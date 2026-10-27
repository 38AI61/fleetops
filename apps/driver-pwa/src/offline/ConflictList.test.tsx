import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createElement } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { transitionJob } from '../api/driver'
import { TODAY_QUERY_KEY } from '../jobs/keys'
import { resetQueueDb } from '../test/queue'
import type { ActionRow } from './db'
import { ConflictList } from './ConflictList'
import { enqueueTransition, listQueue, markConflict } from './queue'

vi.mock('../api/driver', () => ({ listTodayJobs: vi.fn(), transitionJob: vi.fn(), deliverJob: vi.fn() }))

const USER = '7'
const MESSAGE = 'Job changed on the server; not applied.'

// Seeds a conflict for a job and returns the row as it is stored.
async function conflictFor(jobId: number, action: 'accept' | 'start'): Promise<ActionRow> {
  const row = await enqueueTransition(USER, { id: jobId }, action)
  await markConflict(row.seq!, { code: 'conflict', message: MESSAGE })
  return (await listQueue(USER)).find((r) => r.seq === row.seq)!
}

function renderList(client: QueryClient, conflicts: ActionRow[]) {
  return render(
    createElement(
      QueryClientProvider,
      { client },
      createElement(ConflictList, { userId: USER, conflicts }),
    ),
  )
}

describe('ConflictList', () => {
  let client: QueryClient

  beforeEach(async () => {
    await resetQueueDb()
    vi.mocked(transitionJob).mockReset()
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('a conflict row renders its job, action and message', async () => {
    const row = await conflictFor(4, 'accept')
    renderList(client, [row])

    const card = screen.getByText('Job 4').closest('li') as HTMLElement
    expect(within(card).getByText('Accept')).toBeInTheDocument()
    expect(within(card).getByText(MESSAGE)).toBeInTheDocument()
  })

  it('the heading count matches the conflict rows', async () => {
    const first = await conflictFor(4, 'accept')
    const second = await conflictFor(5, 'start')
    renderList(client, [first, second])

    expect(screen.getByRole('heading', { name: '2 need attention' })).toBeInTheDocument()
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  it('Retry sets the row pending and runs the replay', async () => {
    vi.mocked(transitionJob).mockResolvedValue({} as never)
    const row = await conflictFor(4, 'accept')
    renderList(client, [row])

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(transitionJob).toHaveBeenCalledWith(4, 'accept'))
    await waitFor(async () => expect(await listQueue(USER)).toHaveLength(0))
  })

  it('Discard removes the job rows and invalidates the today query', async () => {
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const row = await conflictFor(4, 'accept')
    await enqueueTransition(USER, { id: 4 }, 'start')
    await enqueueTransition(USER, { id: 6 }, 'accept')
    renderList(client, [row])

    fireEvent.click(screen.getByRole('button', { name: 'Discard' }))

    await waitFor(async () =>
      expect((await listQueue(USER)).map((r) => r.jobId)).toEqual([6]),
    )
    expect(invalidate).toHaveBeenCalledWith({ queryKey: TODAY_QUERY_KEY })
  })
})
