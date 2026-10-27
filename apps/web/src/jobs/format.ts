// Distance for the assignment panel (GF-221). Below 1000 m, whole meters. From 1000 m, kilometers to two decimals.
// A value that rounds up to 1000 m reads as km, so the two units never show 1000 m.
export function formatDistance(m: number): string {
  const rounded = Math.round(m)
  if (rounded < 1000) return `${rounded} m`
  return `${(m / 1000).toFixed(2)} km`
}
