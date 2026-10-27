import { deleteDB } from 'idb'
import { closeQueueDb, QUEUE_DB_NAME } from '../offline/db'

// Empties the queue and snapshot stores between tests. The connection closes first,
// so the delete is not blocked by an open handle.
export async function resetQueueDb(): Promise<void> {
  await closeQueueDb()
  await deleteDB(QUEUE_DB_NAME)
}
