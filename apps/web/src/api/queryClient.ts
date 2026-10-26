import { QueryClient } from '@tanstack/react-query'

// One client for the app. Logout clears it so no fleet data survives a session.
export const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1 } },
})
