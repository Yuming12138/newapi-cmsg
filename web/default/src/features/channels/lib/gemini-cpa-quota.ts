import type { Channel } from '../types'

export type GeminiCPAModelQuota = {
  model: string
  remainingPercent: number | null
  resetAt: number | null
}

export type GeminiCPAAccount = {
  id: string
  tier: string | null
  runtimeUnavailable: boolean
  remainingPercent: number | null
  models: GeminiCPAModelQuota[]
  error: string | null
}

export type GeminiCPAQuotaMeta = {
  remainingPercent: number | null
  models: GeminiCPAModelQuota[]
  accounts: GeminiCPAAccount[]
  accountCount: number | null
  availableAccountCount: number | null
  credentialCount: number | null
  updatedAt: number | null
  partial: boolean
  error: string | null
}

function asObject(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function numeric(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  return null
}

function percent(value: unknown): number | null {
  const result = numeric(value)
  return result != null && result >= 0 && result <= 100 ? result : null
}

function text(value: unknown): string | null {
  return typeof value === 'string' && value.trim() ? value.trim() : null
}

function parseModels(value: unknown): GeminiCPAModelQuota[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((raw) => {
    const item = asObject(raw)
    const model = text(item?.model)
    if (!item || !model) return []
    const reset = numeric(item.reset_at)
    return [
      {
        model,
        remainingPercent: percent(item.remaining_percent),
        resetAt: reset != null && reset > 0 ? reset : null,
      },
    ]
  })
}

export function isGeminiCPAChannel(channel: Channel): boolean {
  if (channel.type !== 1 || !channel.base_url) return false
  try {
    if (!new URL(channel.base_url).hostname.toLowerCase().includes('cliproxy'))
      return false
    let mapping: Record<string, unknown> = {}
    try {
      mapping = asObject(JSON.parse(channel.model_mapping || '{}')) ?? {}
    } catch {
      // Match backend behavior: an invalid optional mapping does not hide
      // explicitly configured Gemini models.
    }
    const models = channel.models.split(',').map((name) => name.trim())
    if (
      models.some((name) => name.toLowerCase().startsWith('kimi-')) ||
      Object.values(mapping).some(
        (target) =>
          typeof target === 'string' && target.toLowerCase().startsWith('kimi-')
      )
    ) {
      return false
    }
    return models.some((name) => {
      const target = mapping[name]
      const model =
        typeof target === 'string' && target.trim() ? target.trim() : name
      return model.startsWith('gemini-')
    })
  } catch {
    return false
  }
}

export function parseGeminiCPAQuotaMeta(
  otherInfo: string | null | undefined
): GeminiCPAQuotaMeta | null {
  if (!otherInfo) return null
  try {
    const raw = asObject(asObject(JSON.parse(otherInfo))?.gemini_cpa_quota)
    if (!raw) return null
    const accounts = (Array.isArray(raw.accounts) ? raw.accounts : []).flatMap(
      (value) => {
        const item = asObject(value)
        const id = text(item?.id)
        if (!item || !id) return []
        return [
          {
            id,
            tier: text(item.tier),
            runtimeUnavailable: item.runtime_unavailable === true,
            remainingPercent: percent(item.remaining_percent),
            models: parseModels(item.models),
            error: text(item.error),
          },
        ]
      }
    )
    return {
      remainingPercent: percent(raw.remaining_percent),
      models: parseModels(raw.models),
      accounts,
      accountCount: numeric(raw.account_count),
      availableAccountCount: numeric(raw.available_account_count),
      credentialCount: numeric(raw.credential_count),
      updatedAt: numeric(raw.updated_at),
      partial: raw.partial === true,
      error: text(raw.error),
    }
  } catch {
    return null
  }
}
