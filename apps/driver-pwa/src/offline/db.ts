import { openDB, type DBSchema, type IDBPDatabase } from 'idb'
import type { DriverJob } from '../api/types'

// Offline queue database (GF-236). Version 1 is the only schema in P15.
export const QUEUE_DB_NAME = 'fleetops-driver'

export type QueueAction = 'accept' | 'start' | 'pickup' | 'deliver'
export type RowStatus = 'pending' | 'in_flight' | 'conflict'

// The photo is stored as bytes, not a Blob or File (GF-236). A File is rebuilt at replay.
export interface QueuedPhoto {
  bytes: ArrayBuffer
  type: string
  name: string
  lat: number
  lng: number
}

// seq is assigned by IndexedDB on insert, so a row not yet stored has none.
export interface ActionRow {
  seq?: number
  userId: string
  jobId: number
  action: QueueAction
  key: string
  status: RowStatus
  enqueuedAt: number
  attempts: number
  conflict?: { code: string; message: string }
  photo?: QueuedPhoto
}

export interface SnapshotRow {
  userId: string
  payload: DriverJob[]
  fetchedAt: number
}

interface QueueDB extends DBSchema {
  actions: {
    key: number
    value: ActionRow
    indexes: { by_job: number; by_key: string; by_user: string }
  }
  snapshots: {
    key: string
    value: SnapshotRow
  }
}

export type QueueDatabase = IDBPDatabase<QueueDB>

let dbPromise: Promise<QueueDatabase> | null = null

export function openQueueDb(): Promise<QueueDatabase> {
  dbPromise ??= openDB<QueueDB>(QUEUE_DB_NAME, 1, {
    upgrade(db) {
      const actions = db.createObjectStore('actions', { keyPath: 'seq', autoIncrement: true })
      actions.createIndex('by_job', 'jobId')
      actions.createIndex('by_key', 'key')
      actions.createIndex('by_user', 'userId')
      db.createObjectStore('snapshots', { keyPath: 'userId' })
    },
    // The browser closed the connection. The next call reopens it.
    terminated() {
      dbPromise = null
    },
  })
  return dbPromise
}

// Closes the connection so the database can be deleted or reopened (tests).
export async function closeQueueDb(): Promise<void> {
  const pending = dbPromise
  dbPromise = null
  if (pending) (await pending).close()
}
