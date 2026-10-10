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

export type GeminiCPAQuotaPoolName = 'gemini' | 'claude' | 'gpt-oss'

export type GeminiCPAQuotaPool = {
  name: GeminiCPAQuotaPoolName
  remainingPercent: number | null
  resetAt: number | null
  resetVaries: boolean
}

const QUOTA_POOL_NAMES: GeminiCPAQuotaPoolName[] = [
  'gemini',
  'claude',
  'gpt-oss',
]

function quotaPoolName(model: string): GeminiCPAQuotaPoolName | null {
  for (const name of QUOTA_POOL_NAMES) {
    if (model.startsWith(`${name}-`)) return name
  }
  return null
}

// The upstream supplies model quotas, not named 5h/monthly windows. Keep
// independent families separate and collapse repeated aliases conservatively:
// one account contributes its lowest known quota, never a sum of model quotas.
export function getGeminiCPAAccountQuotaPools(
  models: readonly GeminiCPAModelQuota[]
): GeminiCPAQuotaPool[] {
  const pools = new Map<GeminiCPAQuotaPoolName, GeminiCPAModelQuota[]>()
  for (const model of models) {
    const name = quotaPoolName(model.model)
    if (!name) continue
    const entries = pools.get(name) ?? []
    entries.push(model)
    pools.set(name, entries)
  }
  return QUOTA_POOL_NAMES.flatMap((name) => {
    const entries = pools.get(name)
    if (!entries) return []
    let remainingPercent: number | null = null
    const resets = new Set<number>()
    let resetUnknown = false
    for (const entry of entries) {
      if (entry.remainingPercent == null) continue
      remainingPercent = Math.min(
        remainingPercent ?? entry.remainingPercent,
        entry.remainingPercent
      )
      if (entry.resetAt == null) resetUnknown = true
      else resets.add(entry.resetAt)
    }
    const resetAt = !resetUnknown && resets.size === 1 ? [...resets][0] : null
    return [
      {
        name,
        remainingPercent,
        resetAt,
        resetVaries: resets.size > 1,
      },
    ]
  })
}

export function getGeminiCPAQuotaPoolSummary(
  meta: GeminiCPAQuotaMeta
): GeminiCPAQuotaPool[] {
  if (meta.accounts.length === 0) {
    return meta.error ? [] : getGeminiCPAAccountQuotaPools(meta.models)
  }
  const accounts = meta.accounts.map((account) =>
    getGeminiCPAAccountQuotaPools(account.models).map((pool) => ({
      ...pool,
      remainingPercent: account.error ? null : pool.remainingPercent,
    }))
  )
  return QUOTA_POOL_NAMES.flatMap((name) => {
    const pools = accounts.flatMap((account) =>
      account.filter((pool) => pool.name === name)
    )
    if (pools.length === 0) return []
    let total = 0
    let count = 0
    let resetUnknown = false
    let resetVaries = false
    const resets = new Set<number>()
    for (const pool of pools) {
      if (pool.remainingPercent == null) continue
      total += pool.remainingPercent
      count++
      if (pool.resetAt == null) resetUnknown = true
      else resets.add(pool.resetAt)
      resetVaries ||= pool.resetVaries
    }
    return [
      {
        name,
        remainingPercent: count > 0 ? total / count : null,
        resetAt: !resetUnknown && resets.size === 1 ? [...resets][0] : null,
        resetVaries: resetVaries || resets.size > 1,
      },
    ]
  })
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
