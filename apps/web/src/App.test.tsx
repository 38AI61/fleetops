import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { App } from './App'

it('App renders the login route without crashing', () => {
  window.history.pushState({}, '', '/login')
  render(<App />)
  expect(screen.getByText('Sign in')).toBeInTheDocument()
})
