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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'
import { I18nextProvider } from 'react-i18next'
import type { DashboardProviderBalances } from '../../../types'

const dom = new Window({ url: 'http://localhost' })
for (const [name, value] of Object.entries({
  window: dom,
  document: dom.document,
  navigator: dom.navigator,
  HTMLElement: dom.HTMLElement,
  Node: dom.Node,
  MutationObserver: dom.MutationObserver,
  IS_REACT_ACT_ENVIRONMENT: true,
})) {
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value,
  })
}
const { cleanup, render, within } = await import('@testing-library/react')
const { UpstreamBalancesPanel } = await import('../upstream-balances-panel')
const i18n = createInstance()
await i18n.init({ lng: 'en', fallbackLng: 'en', resources: { en, zh } })
const clients: QueryClient[] = []
afterEach(async () => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  await i18n.changeLanguage('en')
})

function renderBalance(asxs: DashboardProviderBalances['asxs']) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const unavailable = {
    balance: null,
    currency: 'CNY' as const,
    updated_at: 0,
    channel_count: 0,
    account_count: 0,
    synced_account_count: 0,
    partial: false,
  }
  const data: DashboardProviderBalances = {
    asxs,
    kimi: unavailable,
    deepseek: unavailable,
  }
  client.setQueryData(['dashboard', 'provider-balances'], data)
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <UpstreamBalancesPanel />
      </QueryClientProvider>
    </I18nextProvider>
  )
  const row = view.getByText('ASXS').parentElement?.parentElement
  assert.ok(row)
  return within(row)
}

const usageBalance: DashboardProviderBalances['asxs'] = {
  source: 'asxs_usage',
  balance: 57.23,
  currency: 'USD',
  updated_at: 100,
  subscription_count: 0,
  partial: false,
}

describe('ASXS dashboard balance source', () => {
  test('shows the shared allowance with its own label and sync status', () => {
    const row = renderBalance(usageBalance)
    assert.ok(row.getByText("Today's shared quota"))
    assert.ok(row.getByText('$57.23'))
    assert.ok(row.getByText(/^Updated /))
    assert.equal(row.queryByText('Total daily subscription balance'), null)
  })

  test('shows a synced zero as zero dollars', () => {
    const row = renderBalance({ ...usageBalance, balance: 0 })
    assert.ok(row.getByText('$0.00'))
    assert.equal(row.queryByText('Balance temporarily unavailable'), null)
  })

  test('keeps an unsynced allowance unavailable', () => {
    const row = renderBalance({
      ...usageBalance,
      balance: null,
      updated_at: 0,
      partial: true,
    })
    assert.ok(row.getByText('--'))
    assert.ok(row.getByText('Balance temporarily unavailable'))
  })

  test('preserves the label for account billing responses', () => {
    const row = renderBalance({ ...usageBalance, source: undefined })
    assert.ok(row.getByText('Total daily subscription balance'))
    assert.equal(row.queryByText("Today's shared quota"), null)
  })

  test('labels the shared allowance in Chinese', async () => {
    await i18n.changeLanguage('zh')
    const row = renderBalance(usageBalance)
    assert.ok(row.getByText('今日共享额度'))
    assert.ok(row.getByText(/57\.23/))
  })
})
