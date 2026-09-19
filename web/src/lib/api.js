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

// A ceiling, because the page's lifetime is not a bound: a reader panning
// across the country adds one entry per distinct viewport URL and never drops
// one. 64 is roughly a long session's worth of distinct views.
export const CACHE_LIMIT = 64

// inFlight holds { promise, controller, waiters } per URL, not a bare promise:
// callers share one request, so aborting has to be refcounted (see getJSON).
const inFlight = new Map()

// clearCache is the invalidation. The cache below has no TTL of its own, so
// without this the refresh button would be a control that visibly does nothing:
// every call would be answered from a Map populated at page load. Called from
// exactly one place — the reload the map island registers with the freshness
// store (islands/map.js) — and from tests.
export function clearCache() {
  cache.clear()
  inFlight.clear()
}

export function getJSON(url, { retryOn429 = true, signal = null } = {}) {
  if (signal && signal.aborted) return Promise.reject(abortReason(signal))

  if (cache.has(url)) {
    const body = cache.get(url)
    // A hit refreshes recency. Without this the Map is FIFO, and the views a
    // reader keeps returning to are exactly the ones evicted.
    cache.delete(url)
    cache.set(url, body)
    return Promise.resolve(body)
  }

  // Concurrent callers await the SAME promise rather than each starting a
  // request. Without this, one pinch-zoom gesture's dozen moveend events become
  // a dozen requests and burn the whole burst.
  let entry = inFlight.get(url)
  if (!entry) entry = startFetch(url, retryOn429)

  entry.waiters += 1
  return attach(entry, url, signal)
}

function startFetch(url, retryOn429) {
  const controller = new AbortController()
  const entry = { controller, waiters: 0, promise: null }

  entry.promise = fetchOnce(url, retryOn429, controller.signal)
    .then((body) => {
      // An answer nobody is waiting for any more is not written: the caller
      // that asked for it is gone, and a transport that ignores the abort
      // would otherwise seed the cache behind its back.
      if (controller.signal.aborted) throw abortReason(controller.signal)
      cacheSet(url, body)
      return body
    })
    .finally(() => {
      // Cleared on failure too, so a transient error is retryable rather than
      // permanently poisoning this URL for the page's lifetime. Guarded by
      // identity: an abort already removed this entry and a later call may
      // have installed a newer one for the same URL.
      if (inFlight.get(url) === entry) inFlight.delete(url)
    })

  // The last waiter detaching leaves this promise's rejection unobserved;
  // this handler is what keeps an abort from becoming an unhandled rejection.
  entry.promise.catch(() => {})

  inFlight.set(url, entry)
  return entry
}

// attach hands one caller its own view of a shared in-flight request. A caller
// that aborts detaches from the request; the fetch itself is only cancelled
// when that caller was the last one waiting for it.
function attach(entry, url, signal) {
  if (!signal) return entry.promise.finally(() => { entry.waiters -= 1 })

  return new Promise((resolve, reject) => {
    const done = () => {
      signal.removeEventListener('abort', onAbort)
      entry.waiters -= 1
    }
    const onAbort = () => {
      done()
      if (entry.waiters === 0) {
        entry.controller.abort(abortReason(signal))
        if (inFlight.get(url) === entry) inFlight.delete(url)
      }
      reject(abortReason(signal))
    }
    signal.addEventListener('abort', onAbort)
    entry.promise.then(
      (body) => { done(); resolve(body) },
      (err) => { done(); reject(err) },
    )
  })
}

function abortReason(signal) {
  return signal.reason ?? new DOMException('The operation was aborted.', 'AbortError')
}

function cacheSet(url, body) {
  cache.set(url, body)
  // The Map iterates in insertion order and a hit re-inserts, so the first key
  // is the least recently used one.
  while (cache.size > CACHE_LIMIT) cache.delete(cache.keys().next().value)
}

async function fetchOnce(url, retryOn429, signal) {
  let response = await fetch(url, { headers: { Accept: 'application/json' }, signal })

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
    // An abort during the retry delay ends the request. A cancelled pan must
    // not come back and spend a limiter token nobody is waiting on.
    if (signal && signal.aborted) throw abortReason(signal)
    response = await fetch(url, { headers: { Accept: 'application/json' }, signal })
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
