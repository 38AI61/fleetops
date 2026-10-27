import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { App } from './App'

it('App renders the login route without crashing', async () => {
  window.history.pushState({}, '', '/login')
  render(<App />)
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
})

it.each(['/jobs', '/jobs/new'])('%s redirects to login without a session', async (path) => {
  window.history.pushState({}, '', path)
  render(<App />)
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
})
