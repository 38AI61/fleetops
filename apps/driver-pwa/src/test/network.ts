// Sets navigator.onLine for one test. jsdom defaults it to true.
export function setOnline(online: boolean): void {
  Object.defineProperty(navigator, 'onLine', { value: online, configurable: true })
}
