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
import { isAxiosError, isCancel } from 'axios'
import { queryOptions } from '@tanstack/react-query'
import { getCodexRadarOverview } from '../api'

export const CODEX_RADAR_REFRESH_INTERVAL_MS = 10 * 60 * 1000
export const CODEX_RADAR_RECOVERY_INTERVAL_MS = 65 * 1000

export function shouldRetryCodexRadar(
  failureCount: number,
  error: unknown
): boolean {
  if (failureCount >= 2 || isCancel(error) || !isAxiosError(error)) return false
  const status = error.response?.status
  return (
    status === undefined ||
    status === 408 ||
    status === 429 ||
    (status >= 500 && status <= 599)
  )
}

export function codexRadarRetryDelay(
  attemptIndex: number,
  error: unknown
): number {
  const delay = attemptIndex === 0 ? CODEX_RADAR_RECOVERY_INTERVAL_MS : 120_000
  if (!isAxiosError(error)) return delay
  const retryAfter = error.response?.headers?.['retry-after']
  if (typeof retryAfter !== 'string') return delay
  const seconds = Number(retryAfter)
  if (Number.isFinite(seconds) && seconds > 0) {
    return Math.max(delay, seconds * 1000)
  }
  const deadline = Date.parse(retryAfter)
  if (Number.isFinite(deadline)) return Math.max(delay, deadline - Date.now())
  return delay
}

export const codexRadarQueryOptions = queryOptions({
  queryKey: ['dashboard', 'codex-radar', 'overview', 'radar-bench-v2'],
  queryFn: getCodexRadarOverview,
  meta: { errorHandledLocally: true },
  staleTime: CODEX_RADAR_REFRESH_INTERVAL_MS,
  // A cached fallback is usable, but recover it sooner than the normal ten
  // minutes. Background/hidden tabs retain React Query's default pause policy.
  refetchInterval: (query) =>
    query.state.data?.stale
      ? CODEX_RADAR_RECOVERY_INTERVAL_MS
      : CODEX_RADAR_REFRESH_INTERVAL_MS,
  retry: shouldRetryCodexRadar,
  retryDelay: codexRadarRetryDelay,
})
