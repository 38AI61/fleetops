import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { setSession } from '../auth/session'
import { ZonesPage } from './ZonesPage'

// MapLibre needs WebGL, which jsdom lacks (GF-167). The draw logic is tested in draw.test.ts.
vi.mock('../map/MapView', () => ({ MapView: () => null }))

vi.mock('./api', () => ({
  useFences: () => ({
    data: {
      type: 'FeatureCollection',
      features: [
        {
          type: 'Feature',
          id: 1,
          bbox: [13, 52, 13.1, 52.1],
          geometry: { type: 'Polygon', coordinates: [[[13, 52], [13.1, 52], [13.1, 52.1], [13, 52.1], [13, 52]]] },
          properties: { name: 'Depot yard', kind: 'polygon' },
        },
      ],
    },
    isPending: false,
    isError: false,
  }),
  useCreateFence: () => ({ mutate: vi.fn(), isPending: false, isError: false }),
  useDeleteFence: () => ({ mutate: vi.fn(), isError: false }),
}))

function signIn(role: string) {
  setSession({
    accessToken: 'token',
    expiresAt: Date.now() + 60_000,
    claims: { sub: '1', fleet_id: 1, role, exp: Math.floor(Date.now() / 1000) + 60 },
  })
}

function renderPage() {
  render(
    <MemoryRouter>
      <ZonesPage />
    </MemoryRouter>,
  )
}

describe('ZonesPage role controls (README §8, GF-268)', () => {
  afterEach(() => {
    setSession(null)
  })

  it.each(['admin', 'dispatcher'])('%s sees the draw controls and delete', (role) => {
    signIn(role)
    renderPage()
    expect(screen.getByRole('button', { name: 'Draw polygon' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Draw circle' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save zone' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Delete Depot yard' })).toBeInTheDocument()
  })

  it('manager sees the list and no draw controls', () => {
    signIn('manager')
    renderPage()
    expect(screen.getByText('Depot yard')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Draw polygon' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Draw circle' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save zone' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete Depot yard' })).not.toBeInTheDocument()
  })
})
