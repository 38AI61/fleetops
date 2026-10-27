// Proof-of-delivery form helpers (GF-231). The server sniffs the photo bytes and
// is authoritative. The checks here are UX pre-checks on the declared type and size.

export const PHOTO_TYPES = ['image/jpeg', 'image/png', 'image/webp'] as const

// 5 MiB, the cap the deliver route enforces (D-25, GF-199).
export const MAX_PHOTO_BYTES = 5_242_880

export type PhotoCheck = { ok: true } | { ok: false; reason: 'type' | 'size' }

export function checkPhotoFile(file: { type: string; size: number }): PhotoCheck {
  if (!(PHOTO_TYPES as readonly string[]).includes(file.type)) return { ok: false, reason: 'type' }
  if (file.size > MAX_PHOTO_BYTES) return { ok: false, reason: 'size' }
  return { ok: true }
}

// Multipart body for POST /driver/jobs/:id/deliver. The field names are the server's.
export function buildDeliverForm(photo: File, lat: number, lng: number): FormData {
  const form = new FormData()
  form.append('photo', photo)
  form.append('lat', String(lat))
  form.append('lng', String(lng))
  return form
}
