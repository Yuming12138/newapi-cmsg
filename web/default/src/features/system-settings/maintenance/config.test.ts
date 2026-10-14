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
import { describe, test } from 'node:test'
import {
  parseSidebarModulesAdmin,
  serializeSidebarModulesAdmin,
  SIDEBAR_MODULES_DEFAULT,
} from './config'

describe('retired sidebar modules', () => {
  test('keeps playground and chat presets without retired entries in defaults', () => {
    assert.deepEqual(SIDEBAR_MODULES_DEFAULT.chat, {
      enabled: true,
      playground: true,
      chat: true,
    })
    assert.deepEqual(
      parseSidebarModulesAdmin('').chat,
      SIDEBAR_MODULES_DEFAULT.chat
    )
  })

  test('does not restore retired switches from saved backend configuration', () => {
    const config = parseSidebarModulesAdmin(
      JSON.stringify({
        chat: {
          enabled: true,
          playground: false,
          image_generation: true,
          open_webui: true,
          chat: false,
          custom_client: true,
        },
      })
    )
    assert.deepEqual(config.chat, {
      enabled: true,
      playground: false,
      chat: false,
      custom_client: true,
    })
    const saved = JSON.parse(serializeSidebarModulesAdmin(config))
    assert.equal(Object.hasOwn(saved.chat, 'image_generation'), false)
    assert.equal(Object.hasOwn(saved.chat, 'open_webui'), false)
  })

  test('preserves disabled sections and other custom settings', () => {
    const config = parseSidebarModulesAdmin(
      JSON.stringify({
        chat: { enabled: false, image_generation: true },
        console: { enabled: true, token: false },
        custom: { enabled: true, image_generation: false },
      })
    )
    assert.equal(config.chat.enabled, false)
    assert.equal(config.console.token, false)
    assert.equal(config.custom.image_generation, false)
    assert.equal(config.personal.topup, true)
  })
})
