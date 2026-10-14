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
import {
  buildCodexConfigToml,
  DEFAULT_CODEX_MODEL,
} from '../keys/lib/client-config'

export { DEFAULT_CODEX_MODEL }

export const API_BASE_URL = 'https://api.cmsg666.xyz/v1'
export const simpleConfig = buildCodexConfigToml(
  API_BASE_URL,
  undefined,
  'cmsg',
  'minimal'
)
export const recommendedConfig = buildCodexConfigToml(API_BASE_URL)

export const chatApiExample = `curl ${API_BASE_URL}/chat/completions \\
  -H "Authorization: Bearer 自己的密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${DEFAULT_CODEX_MODEL}",
    "messages": [
      {"role": "user", "content": "用三句话说明这个接口怎么用"}
    ]
  }'`
