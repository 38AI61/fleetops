import { describe, expect, it } from 'vitest'
import { MAX_PHOTO_BYTES, buildDeliverForm, checkPhotoFile } from './deliver'

describe('checkPhotoFile', () => {
  it.each(['image/jpeg', 'image/png', 'image/webp'])('accepts %s', (type) => {
    expect(checkPhotoFile({ type, size: 1024 })).toEqual({ ok: true })
  })

  it.each(['image/gif', 'image/heic', 'application/pdf', ''])('rejects type %j', (type) => {
    expect(checkPhotoFile({ type, size: 1024 })).toEqual({ ok: false, reason: 'type' })
  })

  it('accepts a photo exactly at the cap', () => {
    expect(checkPhotoFile({ type: 'image/jpeg', size: MAX_PHOTO_BYTES })).toEqual({ ok: true })
  })

  it('rejects a photo one byte over the cap', () => {
    expect(checkPhotoFile({ type: 'image/jpeg', size: MAX_PHOTO_BYTES + 1 })).toEqual({
      ok: false,
      reason: 'size',
    })
  })
})

describe('buildDeliverForm', () => {
  it('uses the photo, lat and lng field names', async () => {
    const photo = new File(['jpeg-bytes'], 'proof.jpg', { type: 'image/jpeg' })
    const form = buildDeliverForm(photo, 41.0082, 28.9784)

    expect([...form.keys()]).toEqual(['photo', 'lat', 'lng'])
    expect(form.get('lat')).toBe('41.0082')
    expect(form.get('lng')).toBe('28.9784')
    const sent = form.get('photo') as File
    expect(sent.name).toBe('proof.jpg')
    expect(sent.type).toBe('image/jpeg')
  })
})
