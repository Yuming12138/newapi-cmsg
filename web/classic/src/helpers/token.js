/*
Copyright (C) 2025 QuantumNous

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

import { API } from './api';

/**
 * 按需获取单个令牌的真实 key
 * @param {number|string} tokenId
 * @returns {Promise<string>} 返回不带 sk- 前缀的真实 token key
 */
export async function fetchTokenKey(tokenId) {
  const response = await API.post(`/api/token/${tokenId}/key`);
  const { success, data, message } = response.data || {};
  if (!success || !data?.key) {
    throw new Error(message || 'Failed to fetch token key');
  }
  return data.key;
}

/**
 * 批量获取多个令牌的真实 key
 * @param {number[]} tokenIds
 * @returns {Promise<Record<number, string>>} 返回 {id: key} map，key 不带 sk- 前缀
 */
export async function fetchTokenKeysBatch(tokenIds) {
  const response = await API.post('/api/token/batch/keys', { ids: tokenIds });
  const { success, data, message } = response.data || {};
  if (!success || !data?.keys) {
    throw new Error(message || 'Failed to fetch token keys');
  }
  return data.keys;
}

/**
 * 获取可用的 token keys
 * @returns {Promise<string[]>} 返回 active 状态的不带 sk- 前缀的真实 token key 数组
 */
export async function fetchTokenKeys() {
  try {
    const response = await API.get('/api/token/?p=1&size=10');
    const { success, data } = response.data;
    if (!success) throw new Error('Failed to fetch token keys');

    const tokenItems = Array.isArray(data) ? data : data.items || [];
    const activeTokens = tokenItems.filter((token) => token.status === 1);
    const keyResults = await Promise.allSettled(
      activeTokens.map((token) => fetchTokenKey(token.id)),
    );
    return keyResults
      .filter((result) => result.status === 'fulfilled' && result.value)
      .map((result) => result.value);
  } catch (error) {
    console.error('Error fetching token keys:', error);
    return [];
  }
}

/**
 * 获取服务器地址
 * @returns {string} 服务器地址
 */
export function getServerAddress(statusOverride) {
  const serverAddress =
    getStatusServerAddress(statusOverride) || getStoredStatusServerAddress();
  const currentOrigin = getCurrentOrigin();

  if (
    serverAddress &&
    currentOrigin &&
    isPrivateNetworkAddress(serverAddress) &&
    !isPrivateNetworkAddress(currentOrigin)
  ) {
    return currentOrigin;
  }

  return serverAddress || currentOrigin;
}

function getStatusServerAddress(status) {
  return String(
    status?.server_address ||
      status?.serverAddress ||
      status?.data?.server_address ||
      status?.data?.serverAddress ||
      '',
  ).trim();
}

function getStoredStatusServerAddress() {
  try {
    const status = localStorage.getItem('status');
    return status ? getStatusServerAddress(JSON.parse(status)) : '';
  } catch (error) {
    console.error('Failed to parse status from localStorage:', error);
    return '';
  }
}

function getCurrentOrigin() {
  return typeof window === 'undefined' ? '' : window.location.origin;
}

export function isLocalAddress(address) {
  try {
    const { hostname } = new URL(address);
    return ['localhost', '127.0.0.1', '0.0.0.0', '::1'].includes(hostname);
  } catch {
    return false;
  }
}

export function isPrivateNetworkAddress(address) {
  try {
    const { hostname } = new URL(address);
    const normalized = hostname.toLowerCase();
    return (
      isLocalAddress(address) ||
      normalized.startsWith('10.') ||
      normalized.startsWith('192.168.') ||
      /^172\.(1[6-9]|2\d|3[0-1])\./.test(normalized) ||
      normalized.endsWith('.local')
    );
  } catch {
    return false;
  }
}

export function normalizeServerAddress(address) {
  return String(address || '')
    .trim()
    .replace(/\/+$/, '');
}

export function getOpenAIBaseUrl(serverAddress) {
  const baseUrl = normalizeServerAddress(serverAddress);
  if (!baseUrl) return '';
  return baseUrl.endsWith('/v1') ? baseUrl : `${baseUrl}/v1`;
}

export function getClaudeBaseUrl(serverAddress) {
  const baseUrl = normalizeServerAddress(serverAddress);
  if (!baseUrl) return '';
  return baseUrl.endsWith('/v1') ? baseUrl.slice(0, -3) : baseUrl;
}

export function getClaudeCodeModelDefaults(group) {
  const normalizedGroup = String(group || '').toLowerCase();
  if (normalizedGroup.includes('mimo')) {
    return {
      model: 'mimo-v2.5-pro',
      haiku: 'mimo-v2.5-pro',
      sonnet: 'mimo-v2.5-pro',
      opus: 'mimo-v2.5-pro',
    };
  }
  if (normalizedGroup.includes('deepseek')) {
    return {
      model: 'deepseek-v4-pro[1m]',
      haiku: 'deepseek-v4-flash',
      sonnet: 'deepseek-v4-pro[1m]',
      opus: 'deepseek-v4-pro[1m]',
    };
  }
  return {
    model: 'gpt-5.5',
    haiku: 'gpt-5.4-mini',
    sonnet: 'gpt-5.5',
    opus: 'gpt-5.5',
  };
}

export function getCodexModelDefaults(group) {
  const normalizedGroup = String(group || '').toLowerCase();
  if (normalizedGroup.includes('mimo')) {
    return {
      model: 'mimo-v2.5-pro',
      reviewModel: 'mimo-v2.5-pro',
    };
  }
  if (normalizedGroup.includes('deepseek')) {
    return {
      model: 'deepseek-v4-pro[1m]',
      reviewModel: 'deepseek-v4-pro[1m]',
    };
  }
  return {
    model: 'gpt-5.5',
    reviewModel: 'gpt-5.5',
  };
}

export function buildCodexConfigToml(serverAddress, group, providerName = 'cmsg') {
  const baseUrl = getOpenAIBaseUrl(serverAddress);
  const models = getCodexModelDefaults(group);
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
  ].join('\n');
}

export function buildClaudeCodeSettingsJson(apiKey, serverAddress, group) {
  const baseUrl = getClaudeBaseUrl(serverAddress);
  const models = getClaudeCodeModelDefaults(group);
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
    2,
  );
}

export const CHANNEL_CONN_CLIPBOARD_TYPE = 'newapi_channel_conn';

/**
 * @param {string} key - 完整的 API key（含 sk- 前缀）
 * @param {string} url - 服务器地址
 * @returns {string} JSON 格式的连接字符串
 */
export function encodeChannelConnectionString(key, url) {
  return JSON.stringify({
    _type: CHANNEL_CONN_CLIPBOARD_TYPE,
    key,
    url,
  });
}

/**
 * @param {string} text - 剪贴板文本
 * @returns {{ key: string, url: string } | null}
 */
export function parseChannelConnectionString(text) {
  if (!text || typeof text !== 'string') return null;
  try {
    const parsed = JSON.parse(text.trim());
    if (
      parsed &&
      typeof parsed === 'object' &&
      parsed._type === CHANNEL_CONN_CLIPBOARD_TYPE &&
      typeof parsed.key === 'string' &&
      typeof parsed.url === 'string'
    ) {
      return { key: parsed.key, url: parsed.url };
    }
  } catch {
    // not valid JSON
  }
  return null;
}
