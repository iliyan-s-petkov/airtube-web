// The client-side fetch policy.
//
// Per-entity API responses are `Cache-Control: private`, so no shared cache
// absorbs anything and this module is the only cache there is. The series
// endpoints are limited to 1 rps with a burst of 10, and per-area requests are
// counted toward an enumeration budget by DISTINCT area — so a duplicate
// request is not merely wasteful, it spends a budget the user did not intend to
// spend.

// A 429 is retried once, after the delay the server asked for. Deliberately
// not exponential backoff: under a limiter, a page that keeps retrying is the
// storm the limiter exists to stop.
export const RATE_LIMIT_RETRY_MS = 2000

// Ceiling on how long we will ever wait for a single retry. A server (or a CDN
// in front of it) asking for longer than this is telling us it will not serve
// this request soon — waiting it out would hang the page with no explanation,
// which is worse than failing fast. When the server's Retry-After exceeds this,
// we give up immediately rather than sleep the capped time and retry anyway.
const RETRY_AFTER_CAP_MS = 30000

// Keyed by URL, which already encodes (endpoint, entity, metric, period) — the
// four things that identify a distinct response. Lives for the page's lifetime;
// a reload is the invalidation.
const cache = new Map()
const inFlight = new Map()

// clearCache is a test seam. Nothing in the app calls it: a user who wants fresh
// data reloads, and the page's own Cache-Control TTL bounds staleness.
export function clearCache() {
  cache.clear()
  inFlight.clear()
}

export function getJSON(url, { retryOn429 = true } = {}) {
  if (cache.has(url)) return Promise.resolve(cache.get(url))

  // Concurrent callers await the SAME promise rather than each starting a
  // request. Without this, one pinch-zoom gesture's dozen moveend events become
  // a dozen requests and burn the whole burst.
  const pending = inFlight.get(url)
  if (pending) return pending

  const promise = fetchOnce(url, retryOn429)
    .then((body) => {
      cache.set(url, body)
      return body
    })
    .finally(() => {
      // Cleared on failure too, so a transient error is retryable rather than
      // permanently poisoning this URL for the page's lifetime.
      inFlight.delete(url)
    })

  inFlight.set(url, promise)
  return promise
}

async function fetchOnce(url, retryOn429) {
  let response = await fetch(url, { headers: { Accept: 'application/json' } })

  if (response.status === 429 && retryOn429) {
    const retryMs = retryDelayMs(response)
    if (retryMs > RETRY_AFTER_CAP_MS) {
      // The server is asking for longer than we're willing to wait. Fail now
      // instead of sleeping the capped time and retrying anyway — a server
      // that says "try again in a day" is not going to be fixed by us trying
      // again in 30 seconds.
      throw new Error(`${url}: HTTP 429 (Retry-After exceeds ${RETRY_AFTER_CAP_MS}ms cap)`)
    }
    await delay(retryMs)
    response = await fetch(url, { headers: { Accept: 'application/json' } })
  }
  if (!response.ok) {
    // Thrown, not returned as a sentinel: the caller's catch is what leaves the
    // server-rendered fallback in place, and a sentinel would be rendered as
    // data by a caller that forgot to check.
    throw new Error(`${url}: HTTP ${response.status}`)
  }
  return response.json()
}

// Retry-After is legally either a delay in seconds or an HTTP-date. We only
// honour the seconds form; anything else (a date, empty, negative, zero, NaN,
// garbage text, or a missing header) falls back to the fixed delay. Falling
// back to 0/undefined/NaN would hand setTimeout a delay that fires
// immediately — precisely the retry-storm this module exists to prevent.
function retryDelayMs(response) {
  const header = response.headers && response.headers.get ? response.headers.get('Retry-After') : null
  if (header == null || header === '') return RATE_LIMIT_RETRY_MS

  const seconds = Number(header)
  if (!Number.isFinite(seconds) || seconds <= 0) return RATE_LIMIT_RETRY_MS

  return seconds * 1000
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
