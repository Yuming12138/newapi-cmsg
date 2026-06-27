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

type StatusAddressSource = {
  [key: string]: unknown
  server_address?: string | null
  serverAddress?: string | null
  data?: {
    [key: string]: unknown
    server_address?: string | null
    serverAddress?: string | null
  } | null
} | null | undefined

export function isLocalAddress(address: string): boolean {
  try {
    const { hostname } = new URL(address)
    return ['localhost', '127.0.0.1', '0.0.0.0', '::1'].includes(hostname)
  } catch {
    return false
  }
}

export function isPrivateNetworkAddress(address: string): boolean {
  try {
    const { hostname } = new URL(address)
    const normalized = hostname.toLowerCase()
    if (isLocalAddress(address)) return true
    if (normalized.startsWith('10.')) return true
    if (normalized.startsWith('192.168.')) return true
    if (/^172\.(1[6-9]|2\d|3[0-1])\./.test(normalized)) return true
    return normalized.endsWith('.local')
  } catch {
    return false
  }
}

function getStatusServerAddress(status?: StatusAddressSource): string {
  return String(
    status?.server_address ??
      status?.serverAddress ??
      status?.data?.server_address ??
      status?.data?.serverAddress ??
      ''
  ).trim()
}

function getStoredStatusServerAddress(): string {
  try {
    const raw = localStorage.getItem('status')
    if (raw) {
      return getStatusServerAddress(JSON.parse(raw) as StatusAddressSource)
    }
  } catch {
    /* empty */
  }
  return ''
}

function getCurrentOrigin(): string {
  if (typeof window === 'undefined') return ''
  return window.location.origin
}

export function getServerAddress(status?: StatusAddressSource): string {
  const serverAddress =
    getStatusServerAddress(status) || getStoredStatusServerAddress()
  const currentOrigin = getCurrentOrigin()

  if (
    serverAddress &&
    currentOrigin &&
    isPrivateNetworkAddress(serverAddress) &&
    !isPrivateNetworkAddress(currentOrigin)
  ) {
    return currentOrigin
  }

  return serverAddress || currentOrigin
}

export function normalizeServerAddress(address: string): string {
  return String(address || '')
    .trim()
    .replace(/\/+$/, '')
}

export function getOpenAIBaseUrl(serverAddress: string): string {
  const baseUrl = normalizeServerAddress(serverAddress)
  if (!baseUrl) return ''
  return baseUrl.endsWith('/v1') ? baseUrl : `${baseUrl}/v1`
}

export function getClaudeBaseUrl(serverAddress: string): string {
  const baseUrl = normalizeServerAddress(serverAddress)
  if (!baseUrl) return ''
  return baseUrl.endsWith('/v1') ? baseUrl.slice(0, -3) : baseUrl
}

function getClaudeCodeModelDefaults(group?: string | null) {
  const normalizedGroup = String(group || '').toLowerCase()
  if (normalizedGroup.includes('mimo')) {
    return {
      model: 'mimo-v2.5-pro',
      haiku: 'mimo-v2.5-pro',
      sonnet: 'mimo-v2.5-pro',
      opus: 'mimo-v2.5-pro',
    }
  }
  if (normalizedGroup.includes('deepseek')) {
    return {
      model: 'deepseek-v4-pro[1m]',
      haiku: 'deepseek-v4-flash',
      sonnet: 'deepseek-v4-pro[1m]',
      opus: 'deepseek-v4-pro[1m]',
    }
  }
  return {
    model: 'gpt-5.5',
    haiku: 'gpt-5.4-mini',
    sonnet: 'gpt-5.5',
    opus: 'gpt-5.5',
  }
}

function getCodexModelDefaults(group?: string | null) {
  const normalizedGroup = String(group || '').toLowerCase()
  if (normalizedGroup.includes('mimo')) {
    return {
      model: 'mimo-v2.5-pro',
      reviewModel: 'mimo-v2.5-pro',
    }
  }
  if (normalizedGroup.includes('deepseek')) {
    return {
      model: 'deepseek-v4-pro[1m]',
      reviewModel: 'deepseek-v4-pro[1m]',
    }
  }
  return {
    model: 'gpt-5.5',
    reviewModel: 'gpt-5.5',
  }
}

export function buildCodexConfigToml(
  serverAddress: string,
  group?: string | null,
  providerName = 'cmsg'
): string {
  const baseUrl = getOpenAIBaseUrl(serverAddress)
  const models = getCodexModelDefaults(group)
  return [
    `model_provider = "${providerName}"`,
    `model = "${models.model}"`,
    `review_model = "${models.reviewModel}"`,
    'model_reasoning_effort = "xhigh"',
    'disable_response_storage = true',
    'network_access = "enabled"',
    'windows_wsl_setup_acknowledged = true',
    'model_context_window = 400000',
    'model_auto_compact_token_limit = 360000',
    '',
    `[model_providers.${providerName}]`,
    `name = "${providerName}"`,
    `base_url = "${baseUrl}"`,
    'wire_api = "responses"',
    'requires_openai_auth = true',
  ].join('\n')
}

export function buildClaudeCodeSettingsJson(
  apiKey: string,
  serverAddress: string,
  group?: string | null
): string {
  const baseUrl = getClaudeBaseUrl(serverAddress)
  const models = getClaudeCodeModelDefaults(group)
  return JSON.stringify(
    {
      env: {
        CLAUDE_CODE_ATTRIBUTTION_HEADER: '0',
        ANTHROPIC_AUTH_TOKEN: apiKey,
        ANTHROPIC_BASE_URL: baseUrl,
        ANTHROPIC_MODEL: models.model,
        ANTHROPIC_DEFAULT_HAIKU_MODEL: models.haiku,
        ANTHROPIC_DEFAULT_SONNET_MODEL: models.sonnet,
        ANTHROPIC_DEFAULT_OPUS_MODEL: models.opus,
      },
    },
    null,
    2
  )
}
