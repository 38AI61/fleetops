import { apiFetch, expectJson, getJson, sendForm, sendJson } from './client'
import type { DriverJob, TokenResponse } from './types'

// The three one-tap moves with an empty JSON body (GF-229). Fail is not exposed in P14.
export type TransitionAction = 'accept' | 'start' | 'pickup'

// POST /auth/login. The caller applies the token (GF-227).
export async function login(email: string, password: string): Promise<TokenResponse> {
  return expectJson<TokenResponse>(
    await apiFetch('/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    }),
  )
}

export async function listTodayJobs(): Promise<DriverJob[]> {
  const body = await getJson<{ jobs: DriverJob[] }>('/driver/jobs/today')
  return body.jobs
}

export function transitionJob(id: number, action: TransitionAction): Promise<DriverJob> {
  return sendJson<DriverJob>('POST', `/driver/jobs/${id}/${action}`, {})
}

// Multipart POST with the fields built by buildDeliverForm (GF-231).
export function deliverJob(id: number, form: FormData): Promise<DriverJob> {
  return sendForm<DriverJob>('POST', `/driver/jobs/${id}/deliver`, form)
}
