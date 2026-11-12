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
 * CMSG: Artificial Analysis Intelligence Index leaderboard.
 *
 * Static snapshot (not fetched at runtime) so the public model square never
 * depends on a third-party site being reachable. Refresh by re-extracting the
 * `initialModels` RSC payload from
 * https://artificialanalysis.ai/leaderboards/models and updating the list
 * below; keep entries in descending score order.
 *
 * The source page exposes one row per reasoning-effort variant (e.g. "GPT-6
 * Astra (Max)" / "(High)" / "(Low)"); we collapse them to one row per base
 * model, keeping the highest-scoring variant, so ranks stay meaningful.
 *
 * Only models actually listed in our model square are mapped via `ourModel`.
 * Variants without an independent leaderboard entry (hy4free, hy3,
 * deepseek-v4.1-flash-sg, gpt-5.6-*, gpt-6-sol, glm-5.2) are left unmapped.
 */
export const INTELLIGENCE_SOURCE_URL =
  'https://artificialanalysis.ai/leaderboards/models'
export const INTELLIGENCE_SOURCE_NAME = 'Artificial Analysis'
export const INTELLIGENCE_SNAPSHOT_DATE = '2026-11-12'

export interface IntelligenceEntry {
  /** Display name on the Artificial Analysis leaderboard. */
  name: string
  /** Intelligence Index score. */
  score: number
  /** Corresponding model id in our model square, when we offer it. */
  ourModel?: string
}

/** Base models with an Intelligence Index, in descending score order. */
export const INTELLIGENCE_LEADERBOARD: readonly IntelligenceEntry[] = [
  { name: 'Claude Fable 5.2', score: 64.56 },
  { name: 'Gemini 4 Carbon', score: 60.03 },
  { name: 'Claude Opus 5.5', score: 59.11 },
  { name: 'GPT-6 Astra', score: 56.85, ourModel: 'gpt-6-astra' },
  { name: 'GPT-6.1 Sol', score: 54.61, ourModel: 'gpt-6.1-sol' },
  { name: 'Claude Sonnet 5.5', score: 54.54 },
  { name: 'GPT-6.1 Terra', score: 51.33 },
  { name: 'Gemini 4 Argon', score: 50.41 },
  { name: 'Muse Wonder Full', score: 50.08 },
  { name: 'Kimi K3.1 Pro', score: 46.45, ourModel: 'kimi-k3.1-pro' },
  { name: 'Muse Wonder', score: 43.57 },
  { name: 'Claude Haiku 5.5', score: 36.48 },
  { name: 'Muse Ember', score: 36.02 },
  { name: 'Grok 4.7', score: 35.49 },
  { name: 'Tencent Hy4', score: 34.56, ourModel: 'hy4std' },
  { name: 'Qwen3.8 Max', score: 33.65 },
  {
    name: 'DeepSeek V4.1 Flash 1017',
    score: 32.59,
    ourModel: 'deepseek-v4.1-flash',
  },
  { name: 'GLM-5.3', score: 31.65, ourModel: 'glm-5.3' },
  { name: 'Gemini 3.8 Flash', score: 30.85 },
  { name: 'Inkling 2 Small', score: 30.63 },
  { name: 'MiMo-V2.6-Pro', score: 29.89 },
  { name: 'Ling 3.1 Flash 1019', score: 29.57 },
  {
    name: 'MiniMax-M3.1-Flash',
    score: 29.06,
    ourModel: 'minimax-m3.1-flash',
  },
  { name: 'Qwen3.8-Flash-Next', score: 28.58 },
  { name: 'Step 5 Preview', score: 28.53 },
  { name: 'GPT-6 Luna', score: 27.86, ourModel: 'gpt-6-luna' },
  { name: 'GLM-5.3-Flash', score: 27.8, ourModel: 'glm-5.3-flash' },
  { name: 'MiMo-V2.6-Flash', score: 26.73 },
  { name: 'Mistral Large 4', score: 26.12 },
  { name: 'Muse Spark Open', score: 24.88 },
  { name: 'Agnes 2.5 Pro Theta', score: 24.03 },
  { name: 'DeepSeek V4 Pro 0813', score: 23.06, ourModel: 'deepseek-v4-pro' },
  { name: 'Nanbeige4.3-33B', score: 20.41 },
  { name: 'Qwen3.8 27B', score: 20.11 },
  { name: 'JT-4.1 Flash 236B A21B', score: 18.95 },
  { name: 'MiniMax-M3', score: 18.88 },
  { name: 'K2 Horizon 375B A23B', score: 17.82 },
  { name: 'Beam 1.0', score: 17.73 },
  { name: 'Solar Pro 4', score: 14.16 },
  { name: 'Apodex 1.1', score: 13.67 },
  { name: 'Nemotron 3.5 Super VL', score: 13.62 },
  { name: 'Inkling Small', score: 12.91 },
  { name: 'Inkling', score: 12.48 },
  { name: 'Nemotron 3 Ultra 550B A55B', score: 11.76 },
  { name: 'Gemini 3.5 Flash-Lite', score: 11.66 },
  { name: 'K-EXAONE 2.0', score: 7.56 },
  { name: 'Muse Glimmer', score: 6.34 },
  { name: 'Command A+', score: 6.24 },
  { name: 'Nemotron 3.5 Lightning', score: 5.53 },
  { name: 'Granite 4.2 8B', score: 5.1 },
  { name: 'Granite 4.2 3B', score: 4.72 },
  { name: 'Mistral Medium 3.5', score: 4.65 },
  { name: 'Gemma 4 31B', score: 4.38 },
  { name: 'Mistral Small 4', score: 4.3 },
  { name: 'gpt-oss-120b', score: 3.82 },
  { name: 'North Mini Code', score: 3.42 },
  { name: 'NVIDIA Nemotron 3 Nano 4B', score: 1.82 },
]

/** Number of models on the snapshot board (used in the section footer). */
export const INTELLIGENCE_LEADERBOARD_SIZE = INTELLIGENCE_LEADERBOARD.length

/**
 * Bar colours per vendor, in the spirit of the Artificial Analysis chart but
 * tuned for our dark theme.
 * OpenAI is near-white because the source chart paints it black and black is
 * invisible on a dark page.
 */
export const VENDOR_COLORS: Record<string, string> = {
  OpenAI: '#e6e6e6',
  Anthropic: '#d97757',
  Google: '#4285f4',
  DeepSeek: '#4d6bfe',
  'Z AI': '#8b5cf6',
  Moonshot: '#22c55e',
  Tencent: '#06b6d4',
  MiniMax: '#f97316',
  Alibaba: '#6f6cf0',
  xAI: '#94a3b8',
  Meta: '#0866ff',
  Mistral: '#ff7000',
}

/** Extra colours for vendors we did not enumerate, picked by name hash. */
const FALLBACK_COLORS = ['#a3a3a3', '#84cc16', '#ec4899', '#14b8a6']

/**
 * Vendor inferred from the model id. The public pricing payload carries no
 * vendor field, and these prefixes are stable for our catalog.
 */
export function getModelVendor(modelId: string): string {
  const id = modelId.toLowerCase()
  if (id.startsWith('codex') || id.startsWith('gpt')) return 'OpenAI'
  if (id.startsWith('deepseek')) return 'DeepSeek'
  if (id.startsWith('glm')) return 'Z AI'
  if (id.startsWith('minimax')) return 'MiniMax'
  if (id.startsWith('kimi')) return 'Moonshot'
  if (id.startsWith('hy')) return 'Tencent'
  if (id.startsWith('qwen')) return 'Alibaba'
  if (id.startsWith('claude')) return 'Anthropic'
  if (id.startsWith('gemini')) return 'Google'
  if (id.startsWith('grok')) return 'xAI'
  if (id.startsWith('muse')) return 'Meta'
  if (id.startsWith('mistral') || id.startsWith('mixtral')) return 'Mistral'
  return 'Other'
}

export function getVendorColor(vendor: string): string {
  const known = VENDOR_COLORS[vendor]
  if (known) return known
  let hash = 0
  for (let i = 0; i < vendor.length; i++) {
    hash = (hash * 31 + vendor.charCodeAt(i)) % 997
  }
  return FALLBACK_COLORS[hash % FALLBACK_COLORS.length]
}

export interface RankedOurModel extends IntelligenceEntry {
  /** 1-based position in the snapshot leaderboard. */
  rank: number
  /** Our model id. */
  ourModel: string
}

/**
 * Filter the leaderboard down to models we actually offer, preserving the
 * leaderboard order. `availableNames` comes from the live pricing API, so a
 * model that is hidden/removed from the square never shows in the ranking.
 */
export function getRankedOurModels(
  availableNames: readonly string[]
): RankedOurModel[] {
  const available = new Set(availableNames)
  return INTELLIGENCE_LEADERBOARD.flatMap((entry, index) => {
    if (!entry.ourModel || !available.has(entry.ourModel)) return []
    return [
      {
        ...entry,
        ourModel: entry.ourModel,
        rank: index + 1,
      },
    ]
  })
}
