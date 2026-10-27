// Build artifact check (GF-234). Zero dependencies. Runs at the end of `build`.
// Exits 1 with a named failure on the first missing or wrong artifact.
import { existsSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const dist = fileURLToPath(new URL('../dist/', import.meta.url))

function fail(name, detail) {
  console.error(`check-pwa-build: ${name}: ${detail}`)
  process.exit(1)
}

function read(file) {
  const path = join(dist, file)
  if (!existsSync(path)) fail(`${file} missing`, `expected ${path}`)
  return readFileSync(path, 'utf8')
}

// manifest.webmanifest
let manifest
try {
  manifest = JSON.parse(read('manifest.webmanifest'))
} catch (err) {
  fail('manifest.webmanifest', `does not parse as JSON: ${err.message}`)
}
if (typeof manifest.name !== 'string' || manifest.name === '') fail('manifest', 'name missing')
if (typeof manifest.start_url !== 'string' || manifest.start_url === '') fail('manifest', 'start_url missing')
if (manifest.display !== 'standalone') fail('manifest', `display is ${JSON.stringify(manifest.display)}, want "standalone"`)
const sizes = new Set((manifest.icons ?? []).map((icon) => icon.sizes))
for (const size of ['192x192', '512x512']) {
  if (!sizes.has(size)) fail('manifest', `icon ${size} missing`)
}

// sw.js exists and is non-empty
const sw = join(dist, 'sw.js')
if (!existsSync(sw) || statSync(sw).size === 0) fail('sw.js', 'missing or empty')

// registerSW.js exists
read('registerSW.js')

// index.html links the manifest and loads the registration script
const html = read('index.html')
if (!html.includes('manifest.webmanifest')) fail('index.html', 'does not link manifest.webmanifest')
if (!html.includes('registerSW.js')) fail('index.html', 'does not reference registerSW.js')

console.log('check-pwa-build: ok')
