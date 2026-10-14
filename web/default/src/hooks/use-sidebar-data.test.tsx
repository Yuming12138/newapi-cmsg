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
import { createInstance } from 'i18next'
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { useSidebarData } from './use-sidebar-data'

function SidebarLinks() {
  const data = useSidebarData()
  return (
    <nav>
      {data.navGroups.flatMap((group) =>
        group.items.map((item) =>
          'url' in item && item.url ? (
            <a key={item.url} href={item.url}>
              {item.title}
            </a>
          ) : null
        )
      )}
    </nav>
  )
}

test('sidebar preserves active destinations without image or retired WebUI links', async () => {
  const i18n = createInstance()
  await i18n.init({ lng: 'en', fallbackLng: 'en', resources: {} })
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <SidebarLinks />
    </I18nextProvider>
  )
  assert.ok(html.includes('href="/playground"'))
  assert.ok(html.includes('href="/keys"'))
  assert.ok(html.includes('href="/channels"'))
  assert.ok(!html.includes('/image-generation'))
  assert.ok(!html.includes('webui.cmsg666.xyz'))
})
