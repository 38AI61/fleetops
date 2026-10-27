// One read of navigator.geolocation as a promise. Rejects when the API is missing
// or the read fails, so callers decide what a failed read means for them.
export function readPosition(options: PositionOptions): Promise<GeolocationPosition> {
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) {
      reject(new Error('geolocation unavailable'))
      return
    }
    navigator.geolocation.getCurrentPosition(resolve, reject, options)
  })
}
