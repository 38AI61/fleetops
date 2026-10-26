import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { App } from './App'

it('App renders the login route without crashing', async () => {
  window.history.pushState({}, '', '/login')
  render(<App />)
  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
})
