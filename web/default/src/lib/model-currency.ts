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

/**
 * CMSG: which models are billed in CNY on this relay.
 *
 * Domestic vendors quote and settle in RMB, so their prices are shown in ¥ in
 * the model square *and* in the usage logs; everything else stays in the
 * billing currency (USD). Keeping this list in one place stops the two pages
 * from disagreeing (the logs used to show $ for a deepseek upstream).
 *
 * Match on the billing/upstream model name, not the alias the user requested:
 * `gpt-6.1-sol` routed to a `deepseek-v4.1-flash` key is still an RMB cost.
 */
export const CNY_MODEL_PREFIXES = [
  'kimi-',
  'deepseek-',
  'glm-',
  'hy',
  'minimax-',
  'mimo-',
] as const

export function isCNYPricedModel(modelName: string): boolean {
  if (!modelName) return false
  const name = modelName.toLowerCase()
  return CNY_MODEL_PREFIXES.some((prefix) => name.startsWith(prefix))
}
