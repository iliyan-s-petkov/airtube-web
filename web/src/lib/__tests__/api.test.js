import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getJSON, clearCache, RATE_LIMIT_RETRY_MS } from '../api.js'

function jsonResponse(body, status = 200, headers = {}) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name) => (name in headers ? headers[name] : null) },
    json: async () => body,
  }
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

// A network error (fetch itself rejects, e.g. offline / DNS failure) must
// propagate as-is, and must not leave the URL permanently poisoned: the
// in-flight entry has to be cleared so a later call can try again.
it('propagates a network error and clears the in-flight entry', async () => {
  fetchMock.mockRejectedValueOnce(new Error('network down'))
  await expect(getJSON('/api/v1/overview')).rejects.toThrow('network down')

  fetchMock.mockResolvedValueOnce(jsonResponse({ areas: [7] }))
  await expect(getJSON('/api/v1/overview')).resolves.toEqual({ areas: [7] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// A 200 with a body that isn't valid JSON must reject with the SyntaxError
// from response.json(), not resolve to a sentinel, and must not be cached —
// a transient bad body should be retryable on the next call.
it('rejects on malformed JSON and does not cache the failure', async () => {
  fetchMock.mockResolvedValueOnce({
    ok: true,
    status: 200,
    headers: { get: () => null },
    json: async () => {
      throw new SyntaxError('Unexpected token in JSON')
    },
  })
  await expect(getJSON('/api/v1/overview')).rejects.toThrow(SyntaxError)

  fetchMock.mockResolvedValueOnce(jsonResponse({ areas: [8] }))
  await expect(getJSON('/api/v1/overview')).resolves.toEqual({ areas: [8] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// The server's Retry-After header must win over the hardcoded fallback. If it
// didn't, this client would retry too early after the server raises its shed
// interval, spending another token from the bucket it just emptied.
it('honours a numeric Retry-After header over the fixed fallback', async () => {
  fetchMock
    .mockResolvedValueOnce(jsonResponse({}, 429, { 'Retry-After': '5' }))
    .mockResolvedValueOnce(jsonResponse({ areas: [4] }))

  const pending = getJSON('/api/v1/overview')

  // The fixed fallback (2000ms) is not enough time; the header asked for 5s.
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS)
  expect(fetchMock).toHaveBeenCalledTimes(1)

  await vi.advanceTimersByTimeAsync(5000 - RATE_LIMIT_RETRY_MS)
  await expect(pending).resolves.toEqual({ areas: [4] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// Retry-After is in SECONDS; our internal delay is in milliseconds. An
// off-by-1000 here would retry after 1ms instead of 1000ms and hammer the
// limiter it just tripped.
it('converts a Retry-After of 1 second to exactly 1000ms, not 1ms or 1000000ms', async () => {
  fetchMock
    .mockResolvedValueOnce(jsonResponse({}, 429, { 'Retry-After': '1' }))
    .mockResolvedValueOnce(jsonResponse({ areas: [5] }))

  const pending = getJSON('/api/v1/overview')

  await vi.advanceTimersByTimeAsync(999)
  expect(fetchMock).toHaveBeenCalledTimes(1) // not yet — would fail if the header were misread as ms

  await vi.advanceTimersByTimeAsync(1)
  await expect(pending).resolves.toEqual({ areas: [5] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// A Retry-After beyond our ceiling means the server will not serve this
// request soon. We give up immediately rather than sleep the capped time and
// retry anyway: no second fetch, no timer, an immediate rejection.
it('gives up immediately when Retry-After exceeds the cap, without a second fetch', async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({}, 429, { 'Retry-After': '999' }))

  await expect(getJSON('/api/v1/overview')).rejects.toThrow(/429/)
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

// Anything we cannot parse as a positive number of seconds — an HTTP-date, an
// empty string, a negative number, zero, non-numeric text, or a missing
// header — must fall back to RATE_LIMIT_RETRY_MS exactly, never to 0 or NaN
// (a NaN delay fires setTimeout immediately, which is the retry-storm this
// module exists to prevent).
it.each([
  ['missing header', undefined],
  ['empty string', ''],
  ['an HTTP-date', 'Wed, 21 Oct 2026 07:28:00 GMT'],
  ['a negative number', '-5'],
  ['zero', '0'],
  ['non-numeric text', 'banana'],
  ['the literal string NaN', 'NaN'],
])('falls back to RATE_LIMIT_RETRY_MS for Retry-After: %s (%j)', async (_label, headerValue) => {
  const headers = headerValue === undefined ? {} : { 'Retry-After': headerValue }
  fetchMock
    .mockResolvedValueOnce(jsonResponse({}, 429, headers))
    .mockResolvedValueOnce(jsonResponse({ areas: [6] }))

  const pending = getJSON('/api/v1/overview')

  // One tick short of the fallback: must NOT have retried yet. If a bad
  // header value were coerced to 0/NaN, setTimeout would fire immediately and
  // this assertion would already see 2 calls.
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS - 1)
  expect(fetchMock).toHaveBeenCalledTimes(1)

  await vi.advanceTimersByTimeAsync(1)
  await expect(pending).resolves.toEqual({ areas: [6] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})
