import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import { buildDeliverForm, checkPhotoFile } from '../jobs/deliver'
import { useDeliverJob } from '../jobs/queries'
import { readPosition } from '../position/geo'

const PHOTO_MESSAGES = {
  type: 'Photo must be a JPEG, PNG or WebP image.',
  size: 'Photo is too large',
} as const
const LOCATION_FAILED = 'Location is needed to record the delivery. Turn on location and try again.'
const GENERIC_FAILED = 'Could not record the delivery. Try again.'

// Photo capture (GF-231). capture="environment" opens the camera on mobile.
export function DeliverPage() {
  const params = useParams()
  const navigate = useNavigate()
  const deliver = useDeliverJob()
  const [photo, setPhoto] = useState<File | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const id = Number(params.id)
  const validId = Number.isInteger(id) && id > 0

  function onPhotoChange(file: File | undefined) {
    setNotice(null)
    if (!file) {
      setPhoto(null)
      return
    }
    const check = checkPhotoFile(file)
    if (!check.ok) {
      setPhoto(null)
      setNotice(PHOTO_MESSAGES[check.reason])
      return
    }
    setPhoto(file)
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!photo || !validId) return
    setNotice(null)
    let fix: GeolocationPosition
    try {
      fix = await readPosition({ enableHighAccuracy: true, timeout: 10_000 })
    } catch {
      setNotice(LOCATION_FAILED)
      return
    }
    const form = buildDeliverForm(photo, fix.coords.latitude, fix.coords.longitude)
    deliver.mutate(
      { id, form },
      {
        onSuccess: () => navigate('/today', { replace: true }),
        onError: (err) => setNotice(deliverMessage(err)),
      },
    )
  }

  if (!validId) {
    return (
      <main>
        <h1>Deliver</h1>
        <p role="alert">Job not found.</p>
      </main>
    )
  }

  return (
    <main>
      <h1>Deliver job {id}</h1>
      <form onSubmit={(event) => void onSubmit(event)}>
        <label htmlFor="proof-photo">Proof photo</label>
        <input
          id="proof-photo"
          name="photo"
          type="file"
          accept="image/jpeg,image/png,image/webp"
          capture="environment"
          onChange={(event) => onPhotoChange(event.target.files?.[0])}
        />
        {notice && <p role="alert">{notice}</p>}
        <button type="submit" disabled={!photo || deliver.isPending}>
          Confirm delivery
        </button>
      </form>
    </main>
  )
}

function deliverMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 409) return 'Job changed; refreshed'
    if (err.status === 413) return PHOTO_MESSAGES.size
    if (err.status === 503) return 'Proof storage is unavailable. Try again.'
  }
  return GENERIC_FAILED
}
