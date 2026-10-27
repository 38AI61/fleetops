import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { LatLng } from '../api/types'
import { JobCreateForm, type JobCreateFormProps } from './JobCreateForm'

const pickup: LatLng = { lat: 41.0082, lng: 28.9784 }
const dropoff: LatLng = { lat: 41.0422, lng: 29.0083 }

function renderForm(over: Partial<JobCreateFormProps> = {}) {
  const props: JobCreateFormProps = {
    pickup: null,
    dropoff: null,
    onClearPickup: vi.fn(),
    onClearDropoff: vi.fn(),
    onSubmit: vi.fn(),
    pending: false,
    ...over,
  }
  render(<JobCreateForm {...props} />)
  return props
}

const submit = () => screen.getByRole('button', { name: 'Create job' }) as HTMLButtonElement

describe('JobCreateForm', () => {
  it('shows pickup and dropoff errors and keeps submit disabled until both points are set', () => {
    renderForm()
    expect(screen.getByText('Pick a pickup point on the map')).toBeInTheDocument()
    expect(screen.getByText('Pick a dropoff point on the map')).toBeInTheDocument()
    expect(submit()).toBeDisabled()
  })

  it('enables submit once both points are set by props', () => {
    renderForm({ pickup, dropoff })
    expect(screen.queryByRole('alert')).toBeNull()
    expect(submit()).toBeEnabled()
  })

  it('wires window, notes, size and weight through to the onSubmit body', () => {
    const props = renderForm({ pickup, dropoff })
    fireEvent.change(screen.getByLabelText('Window start'), { target: { value: '2026-10-27T10:00' } })
    fireEvent.change(screen.getByLabelText('Window end'), { target: { value: '2026-10-27T12:00' } })
    fireEvent.change(screen.getByLabelText('Notes'), { target: { value: 'Ring twice' } })
    fireEvent.change(screen.getByLabelText('Size'), { target: { value: 'large' } })
    fireEvent.change(screen.getByLabelText('Weight (kg)'), { target: { value: '12.5' } })
    fireEvent.click(submit())

    expect(props.onSubmit).toHaveBeenCalledTimes(1)
    expect(props.onSubmit).toHaveBeenCalledWith({
      pickup,
      dropoff,
      window_start: new Date('2026-10-27T10:00').toISOString(),
      window_end: new Date('2026-10-27T12:00').toISOString(),
      notes: 'Ring twice',
      size_class: 'large',
      weight_kg: 12.5,
    })
  })

  it('blocks notes over 1000 runes with an inline error and a disabled submit', () => {
    const props = renderForm({ pickup, dropoff })
    fireEvent.change(screen.getByLabelText('Notes'), { target: { value: 'é'.repeat(1001) } })
    expect(screen.getByText('1001 / 1000')).toBeInTheDocument()
    expect(screen.getByText('Notes must be 1000 characters or fewer')).toBeInTheDocument()
    expect(submit()).toBeDisabled()
    fireEvent.click(submit())
    expect(props.onSubmit).not.toHaveBeenCalled()
  })

  it('disables submit while a request is pending', () => {
    const props = renderForm({ pickup, dropoff, pending: true })
    expect(submit()).toBeDisabled()
    fireEvent.submit(screen.getByRole('form', { name: 'New job' }))
    expect(props.onSubmit).not.toHaveBeenCalled()
  })

  it('clear buttons call back and disable when the point is unset', () => {
    const props = renderForm({ pickup, dropoff: null })
    expect(screen.getByRole('button', { name: 'Clear dropoff' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Clear pickup' }))
    expect(props.onClearPickup).toHaveBeenCalledTimes(1)
  })
})
