import { QueryClientProvider } from '@tanstack/react-query'
import { lazy, Suspense, useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { queryClient } from './api/queryClient'
import { bootSession } from './auth/boot'
import { LoginRoute, RequireSession } from './auth/guard'

// Lazy so the login route never loads MapLibre (jsdom cannot run it, and the
// login bundle stays small).
const MapPage = lazy(() => import('./map/MapPage').then((m) => ({ default: m.MapPage })))
const JobsPage = lazy(() => import('./jobs/JobsPage').then((m) => ({ default: m.JobsPage })))
const NewJobPage = lazy(() => import('./jobs/NewJobPage').then((m) => ({ default: m.NewJobPage })))
const ZonesPage = lazy(() => import('./geofences/ZonesPage').then((m) => ({ default: m.ZonesPage })))

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
                <Suspense fallback={<p>Loading</p>}>
                  <MapPage />
                </Suspense>
              </RequireSession>
            }
          />
          <Route
            path="/jobs"
            element={
              <RequireSession>
                <Suspense fallback={<p>Loading</p>}>
                  <JobsPage />
                </Suspense>
              </RequireSession>
            }
          />
          <Route
            path="/jobs/new"
            element={
              <RequireSession>
                <Suspense fallback={<p>Loading</p>}>
                  <NewJobPage />
                </Suspense>
              </RequireSession>
            }
          />
          <Route
            path="/zones"
            element={
              <RequireSession>
                <Suspense fallback={<p>Loading</p>}>
                  <ZonesPage />
                </Suspense>
              </RequireSession>
            }
          />
          <Route path="/" element={<Navigate to="/map" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
