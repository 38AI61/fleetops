import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { queryClient } from './api/queryClient'

// G1 placeholders. G2 and G5 replace these elements with the auth and map pages.
export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<p>Sign in</p>} />
          <Route path="/map" element={<p>Map</p>} />
          <Route path="/" element={<Navigate to="/map" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
