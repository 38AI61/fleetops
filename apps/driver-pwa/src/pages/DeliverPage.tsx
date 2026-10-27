import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import { buildDeliverForm, checkPhotoFile } from '../jobs/deliver'
import { useDeliverJob } from '../jobs/queries'
import { enqueueDeliver } from '../offline/queue'
import { readPosition } from '../position/geo'
import { useAuthState } from '../session/session'

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
  const userId = useAuthState().session?.claims.sub ?? ''
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

  // The photo and coordinates go to IDB, and the driver goes back to Today with a notice (GF-241).
  async function queueDelivery(file: File, lat: number, lng: number) {
    try {
      await enqueueDeliver(userId, { id }, file, lat, lng)
    } catch {
      setNotice(GENERIC_FAILED)
      return
    }
    navigate('/today', { replace: true, state: { queued: true } })
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
    const { latitude: lat, longitude: lng } = fix.coords
    if (!navigator.onLine) {
      await queueDelivery(photo, lat, lng)
      return
    }
    try {
      await deliver.mutateAsync({ id, form: buildDeliverForm(photo, lat, lng) })
      navigate('/today', { replace: true })
    } catch (err) {
      // A network failure means the request never reached the server, so it is queued.
      if (err instanceof TypeError) {
        await queueDelivery(photo, lat, lng)
        return
      }
      setNotice(deliverMessage(err))
    }
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
