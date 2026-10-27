import { fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { deliverJob } from '../api/driver'
import { DEVICE_KEY_STORAGE } from '../device/key'
import { setSession } from '../session/session'
import { listQueue } from '../offline/queue'
import { renderAt } from '../test/render'
import { resetQueueDb } from '../test/queue'
import { setOnline } from '../test/network'
import { signInAs } from '../test/session'

vi.mock('../api/driver', () => ({
  listTodayJobs: vi.fn(async () => []),
  transitionJob: vi.fn(),
  deliverJob: vi.fn(),
}))

// Fixture only: id 12, secret of 43 'A' characters.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

type Geo = Pick<Geolocation, 'getCurrentPosition'>

function stubGeolocation(geo: Geo) {
  Object.defineProperty(navigator, 'geolocation', { value: geo, configurable: true })
}

function fixOk(): Geo {
  return {
    getCurrentPosition: (success: PositionCallback) =>
      success({
        coords: { latitude: 41.0082, longitude: 28.9784, accuracy: 5 } as GeolocationCoordinates,
        timestamp: Date.now(),
      } as GeolocationPosition),
  }
}

function fixFails(): Geo {
  return {
    getCurrentPosition: (_success: PositionCallback, error?: PositionErrorCallback) =>
      error?.({ code: 1, message: 'denied' } as GeolocationPositionError),
  }
}

function choosePhoto(type = 'image/jpeg') {
  const file = new File(['proof'], 'proof.jpg', { type })
  fireEvent.change(screen.getByLabelText('Proof photo'), { target: { files: [file] } })
  return file
}

function submit() {
  fireEvent.click(screen.getByRole('button', { name: 'Confirm delivery' }))
}

describe('DeliverPage', () => {
  beforeEach(async () => {
    await resetQueueDb()
    localStorage.clear()
    localStorage.setItem(DEVICE_KEY_STORAGE, VALID)
    signInAs('driver')
    vi.mocked(deliverJob).mockReset()
    stubGeolocation(fixOk())
  })

  afterEach(() => {
    setOnline(true)
    setSession(null)
    localStorage.clear()
    Reflect.deleteProperty(navigator, 'geolocation')
  })

  it('blocks submit until a photo is chosen', () => {
    renderAt('/jobs/5/deliver')
    expect(screen.getByRole('button', { name: 'Confirm delivery' })).toBeDisabled()
    submit()
    expect(deliverJob).not.toHaveBeenCalled()
  })

  it('rejects a photo of the wrong type with a message and keeps submit blocked', () => {
    renderAt('/jobs/5/deliver')
    choosePhoto('image/gif')
    expect(screen.getByText('Photo must be a JPEG, PNG or WebP image.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Confirm delivery' })).toBeDisabled()
  })

  it('blocks submit with a message when geolocation fails', async () => {
    stubGeolocation(fixFails())
    renderAt('/jobs/5/deliver')
    choosePhoto()

    submit()

    expect(
      await screen.findByText(
        'Location is needed to record the delivery. Turn on location and try again.',
      ),
    ).toBeInTheDocument()
    expect(deliverJob).not.toHaveBeenCalled()
  })

  it('posts the photo and coordinates, then returns to /today', async () => {
    vi.mocked(deliverJob).mockResolvedValue({} as never)
    renderAt('/jobs/5/deliver')
    choosePhoto()

    submit()

    expect(await screen.findByRole('heading', { name: 'Today' })).toBeInTheDocument()
    const [id, form] = vi.mocked(deliverJob).mock.calls[0]
    expect(id).toBe(5)
    expect(form.get('lat')).toBe('41.0082')
    expect(form.get('lng')).toBe('28.9784')
    expect(form.get('photo')).toBeInstanceOf(File)
  })

  it('an offline submit queues the photo and does not call the API', async () => {
    setOnline(false)
    renderAt('/jobs/5/deliver')
    choosePhoto()

    submit()

    expect(await screen.findByRole('heading', { name: 'Today' })).toBeInTheDocument()
    expect(screen.getByText(/Delivery queued/)).toBeInTheDocument()
    expect(deliverJob).not.toHaveBeenCalled()
    const [queued] = await listQueue('7')
    expect(queued).toMatchObject({ jobId: 5, action: 'deliver', key: '5:deliver' })
    expect(queued.photo?.name).toBe('proof.jpg')
  })

  it('a network failure on submit queues the delivery', async () => {
    vi.mocked(deliverJob).mockRejectedValue(new TypeError('Failed to fetch'))
    renderAt('/jobs/5/deliver')
    choosePhoto()

    submit()

    expect(await screen.findByRole('heading', { name: 'Today' })).toBeInTheDocument()
    expect(await listQueue('7')).toHaveLength(1)
  })

  it.each([
    [409, 'Job changed; refreshed'],
    [413, 'Photo is too large'],
    [503, 'Proof storage is unavailable. Try again.'],
  ])('maps status %i to its message', async (status, message) => {
    vi.mocked(deliverJob).mockRejectedValue(new ApiError(status, 'code'))
    renderAt('/jobs/5/deliver')
    choosePhoto()

    submit()

    expect(await screen.findByText(message)).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Today' })).toBeNull())
  })
})
