import { Navigate, Route, Routes } from 'react-router'
import { DeliverPage } from './pages/DeliverPage'
import { KeySetupPage } from './pages/KeySetupPage'
import { LoginPage } from './pages/LoginPage'
import { TodayPage } from './pages/TodayPage'
import { RequireDeviceKey, RequireSession } from './routes/guards'

// Routes (GF-230). /login is open. Everything else needs a driver session, and
// the job routes also need a stored device key.
export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<RequireSession />}>
        <Route path="/setup-key" element={<KeySetupPage />} />
        <Route element={<RequireDeviceKey />}>
          <Route path="/today" element={<TodayPage />} />
          <Route path="/jobs/:id/deliver" element={<DeliverPage />} />
        </Route>
      </Route>
      <Route path="/" element={<Navigate to="/today" replace />} />
      <Route path="*" element={<Navigate to="/today" replace />} />
    </Routes>
  )
}
