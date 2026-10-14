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
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { describe, test } from 'node:test'
import {
  API_BASE_URL,
  chatApiExample,
  recommendedConfig,
  simpleConfig,
} from '../../../docs/config-examples'
import {
  buildCodexConfigToml,
  DEFAULT_CODEX_MODEL,
  getOpenAIBaseUrl,
} from '../client-config'

type CodexConfig = {
  model: string
  review_model: string
  model_provider: string
  model_reasoning_effort: string
  model_context_window: number
  model_auto_compact_token_limit: number
  sandbox_mode?: string
  approval_policy?: string
  web_search?: string
  sandbox_workspace_write?: { network_access: boolean }
  model_providers: Record<
    string,
    {
      name: string
      base_url: string
      wire_api: string
      requires_openai_auth: boolean
    }
  >
}

// These tests run with Bun, whose TOML parser validates the actual copied text.
const { TOML } = createRequire(import.meta.url)('bun') as {
  TOML: { parse: (text: string) => CodexConfig }
}

describe('Codex client configuration', () => {
  test('uses the current default for both sessions and review', () => {
    assert.equal(DEFAULT_CODEX_MODEL, 'gpt-6.1-sol')
    for (const group of [undefined, 'cmsg', 'asxs', 'asxs-gpt56-direct']) {
      const config = TOML.parse(buildCodexConfigToml(API_BASE_URL, group))
      assert.equal(config.model, DEFAULT_CODEX_MODEL)
      assert.equal(config.review_model, DEFAULT_CODEX_MODEL)
      assert.equal(config.model_reasoning_effort, 'xhigh')
    }
  })

  test('preserves provider-specific defaults for DeepSeek and MiMo keys', () => {
    for (const [group, model] of [
      ['DEEPSEEK', 'deepseek-v4-pro[1m]'],
      ['mimo-pro', 'mimo-v2.5-pro'],
    ]) {
      const config = TOML.parse(buildCodexConfigToml(API_BASE_URL, group))
      assert.equal(config.model, model)
      assert.equal(config.review_model, model)
    }
  })

  test('normalizes the base URL without appending a second v1', () => {
    for (const address of [
      'https://api.cmsg666.xyz',
      'https://api.cmsg666.xyz/',
      API_BASE_URL,
      `${API_BASE_URL}/`,
    ]) {
      assert.equal(getOpenAIBaseUrl(address), API_BASE_URL)
      const config = TOML.parse(buildCodexConfigToml(address))
      assert.equal(config.model_providers.cmsg.base_url, API_BASE_URL)
    }
  })

  test('keeps provider fields in their own TOML table', () => {
    const config = TOML.parse(
      buildCodexConfigToml(API_BASE_URL, 'cmsg', 'gateway')
    )
    assert.equal(config.model_provider, 'gateway')
    assert.deepEqual(config.model_providers.gateway, {
      name: 'gateway',
      base_url: API_BASE_URL,
      wire_api: 'responses',
      requires_openai_auth: true,
    })
    assert.equal(config.sandbox_workspace_write?.network_access, true)
  })

  test('preserves a context budget and earlier automatic compaction in both presets', () => {
    for (const text of [simpleConfig, recommendedConfig]) {
      const config = TOML.parse(text)
      assert.equal(config.model_context_window, 400000)
      assert.equal(config.model_auto_compact_token_limit, 360000)
      assert.ok(
        config.model_auto_compact_token_limit < config.model_context_window
      )
    }
  })

  test('uses project-scoped permissions in the recommended preset', () => {
    const config = TOML.parse(recommendedConfig)
    assert.equal(config.sandbox_mode, 'workspace-write')
    assert.equal(config.approval_policy, 'on-request')
    assert.equal(config.web_search, 'cached')
    assert.equal(TOML.parse(simpleConfig).sandbox_mode, undefined)
  })

  test('omits obsolete feature flags and top-level network settings', () => {
    for (const text of [simpleConfig, recommendedConfig]) {
      const config = TOML.parse(text)
      for (const key of [
        'features',
        'network_access',
        'disable_response_storage',
        'windows_wsl_setup_acknowledged',
      ]) {
        assert.equal(Object.hasOwn(config, key), false, key)
      }
    }
  })

  test('shares documented examples with the key-page generator', () => {
    assert.equal(recommendedConfig, buildCodexConfigToml(API_BASE_URL))
    assert.equal(
      simpleConfig,
      buildCodexConfigToml(API_BASE_URL, undefined, 'cmsg', 'minimal')
    )
    assert.ok(chatApiExample.includes(`"model": "${DEFAULT_CODEX_MODEL}"`))
  })
})
