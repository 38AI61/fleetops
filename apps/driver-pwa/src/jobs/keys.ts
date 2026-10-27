// The today query key. It lives apart from queries.ts so replay.ts can invalidate it
// without an import cycle (queries.ts imports replay.ts).
export const TODAY_QUERY_KEY = ['driver', 'jobs', 'today'] as const
