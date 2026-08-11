import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getJSON, clearCache, RATE_LIMIT_RETRY_MS } from '../api.js'

function jsonResponse(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body }
}

let fetchMock
beforeEach(() => {
  clearCache()
  fetchMock = vi.fn()
  globalThis.fetch = fetchMock
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// In-flight dedup. Two map layers wanting the same overview during one zoom
// gesture must cost one request, not two: the second would spend a limiter token
// and, on a per-area endpoint, an enumeration observation for an area the user
// looked at once.
it('deduplicates concurrent requests for the same URL', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ areas: [] }))

  const [a, b] = await Promise.all([getJSON('/api/v1/overview'), getJSON('/api/v1/overview')])

  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(a).toBe(b) // the same resolved value, not merely an equal one
})

// Client cache. Zooming out to z<9 and back in must re-render from memory with
// zero requests — which is also why the map caches the overview across tier
// changes rather than refetching per zoom event.
it('serves a repeat request from cache with no fetch', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ areas: [1] }))

  await getJSON('/api/v1/overview')
  const second = await getJSON('/api/v1/overview')

  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(second).toEqual({ areas: [1] })
})

// A 429 is retried ONCE after a fixed delay. Not exponential backoff: a page
// that keeps retrying under a limiter is the storm the limiter exists to stop.
it('retries a 429 exactly once and then succeeds', async () => {
  fetchMock
    .mockResolvedValueOnce(jsonResponse({ error: 'rate_limited' }, 429))
    .mockResolvedValueOnce(jsonResponse({ areas: [2] }))

  const pending = getJSON('/api/v1/overview')
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS)

  await expect(pending).resolves.toEqual({ areas: [2] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

it('gives up after one retry and rejects', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ error: 'rate_limited' }, 429))

  const pending = getJSON('/api/v1/overview')
  const assertion = expect(pending).rejects.toThrow(/429/)
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS)
  await assertion

  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// A failure must not be cached. Caching it would make one transient 503 mean a
// permanently empty map for the rest of the page's life.
it('does not cache a failure', async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({}, 503))
  await expect(getJSON('/api/v1/overview', { retryOn429: false })).rejects.toThrow()

  fetchMock.mockResolvedValueOnce(jsonResponse({ areas: [3] }))
  await expect(getJSON('/api/v1/overview')).resolves.toEqual({ areas: [3] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})
