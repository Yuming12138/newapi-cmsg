/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { AxiosError, AxiosHeaders, CanceledError } from 'axios'
import { QueryClient } from '@tanstack/react-query'
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import type { CodexRadarOverview } from '../../types'
import {
  CODEX_RADAR_RECOVERY_INTERVAL_MS,
  codexRadarQueryOptions,
  codexRadarRetryDelay,
  shouldRetryCodexRadar,
} from '../codex-radar-query'

function failure(status?: number, retryAfter?: string): AxiosError {
  const error = new AxiosError('Radar request failed')
  if (status !== undefined) {
    error.response = {
      status,
      statusText: 'Failure',
      data: {},
      config: { headers: new AxiosHeaders() },
      headers: retryAfter ? { 'retry-after': retryAfter } : {},
    }
  }
  return error
}

describe('Codex Radar bounded recovery', () => {
  test('retries transient transport and server errors, never auth or cancellation', () => {
    for (const status of [undefined, 408, 429, 500, 502, 503, 504]) {
      assert.equal(shouldRetryCodexRadar(0, failure(status)), true)
      assert.equal(shouldRetryCodexRadar(1, failure(status)), true)
      assert.equal(shouldRetryCodexRadar(2, failure(status)), false)
    }
    for (const status of [400, 401, 403, 404, 422]) {
      assert.equal(shouldRetryCodexRadar(0, failure(status)), false)
    }
    assert.equal(shouldRetryCodexRadar(0, new CanceledError()), false)
    assert.equal(shouldRetryCodexRadar(0, new Error('bad data')), false)
  })

  test('waits beyond the backend backoff and respects upstream Retry-After', () => {
    assert.equal(codexRadarRetryDelay(0, failure(502)), 65_000)
    assert.equal(codexRadarRetryDelay(1, failure(502)), 120_000)
    assert.equal(codexRadarRetryDelay(0, failure(502, '60')), 65_000)
    assert.equal(codexRadarRetryDelay(0, failure(429, '180')), 180_000)
    assert.equal(codexRadarRetryDelay(0, failure(502, 'invalid')), 65_000)
    const deadline = new Date(Date.now() + 180_000).toUTCString()
    const delay = codexRadarRetryDelay(0, failure(429, deadline))
    assert.ok(delay > 175_000 && delay <= 180_000)
  })

  test('recovers on the third attempt but never makes a fourth', async () => {
    const client = new QueryClient()
    let calls = 0
    try {
      const result = await client.fetchQuery({
        ...codexRadarQueryOptions,
        retryDelay: 1,
        queryFn: async () => {
          calls++
          if (calls < 3) throw failure(502)
          return { stale: false, metrics: [] } as unknown as CodexRadarOverview
        },
      })
      assert.equal(calls, 3)
      assert.equal(result.stale, false)
      client.clear()
      calls = 0
      await assert.rejects(
        client.fetchQuery({
          ...codexRadarQueryOptions,
          retryDelay: 1,
          queryFn: async () => {
            calls++
            throw failure(502)
          },
        })
      )
      assert.equal(calls, 3)
    } finally {
      client.clear()
    }
  })

  test('preserves existing data after a failed background refresh', async () => {
    const client = new QueryClient()
    const data = {
      stale: false,
      fetched_at: '2026-10-14T00:00:00Z',
      metrics: [],
    } as unknown as CodexRadarOverview
    client.setQueryData(codexRadarQueryOptions.queryKey, data)
    try {
      await assert.rejects(
        client.fetchQuery({
          ...codexRadarQueryOptions,
          staleTime: 0,
          retryDelay: 1,
          queryFn: async () => {
            throw failure(502)
          },
        })
      )
      assert.equal(client.getQueryData(codexRadarQueryOptions.queryKey), data)
      assert.equal(
        client.getQueryState(codexRadarQueryOptions.queryKey)?.status,
        'error'
      )
      assert.equal(CODEX_RADAR_RECOVERY_INTERVAL_MS, 65_000)
    } finally {
      client.clear()
    }
  })
})
