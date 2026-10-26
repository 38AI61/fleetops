import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'

// Vitest globals are off, so Testing Library's auto-cleanup does not run.
afterEach(() => {
  cleanup()
})
