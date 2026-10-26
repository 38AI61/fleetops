import { QueryClientProvider } from '@tanstack/react-query'
import { useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { queryClient } from './api/queryClient'
import { bootSession } from './auth/boot'
import { LoginRoute, RequireSession } from './auth/guard'

// G5 replaces the /map placeholder with the operations map page.
export function App() {
  useEffect(() => {
    void bootSession()
  }, [])

  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginRoute />} />
          <Route
            path="/map"
            element={
              <RequireSession>
                <p>Map</p>
              </RequireSession>
            }
          />
          <Route path="/" element={<Navigate to="/map" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
