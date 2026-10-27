import { QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { App } from './App'
import { queryClient } from './api/queryClient'
import { bootSession } from './session/boot'
import { QueueReplay } from './offline/QueueReplay'

// The refresh cookie restores the in-memory session before the guards decide.
void bootSession()
// Best-effort (GF-243): a refusal is not an error.
void navigator.storage?.persist?.()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
        <QueueReplay />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
