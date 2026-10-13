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
import { AxiosError } from 'axios'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'
import { I18nextProvider } from 'react-i18next'
import { api } from '@/lib/api'
import type { CodexRadarMetric, CodexRadarOverview } from '../../../types'

const dom = new Window({ url: 'http://localhost' })
for (const [name, value] of Object.entries({
  window: dom,
  document: dom.document,
  navigator: dom.navigator,
  HTMLElement: dom.HTMLElement,
  Node: dom.Node,
  Event: dom.Event,
  MutationObserver: dom.MutationObserver,
  getComputedStyle: dom.getComputedStyle.bind(dom),
  IS_REACT_ACT_ENVIRONMENT: true,
})) {
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value,
  })
}
const { cleanup, render, within, waitFor } =
  await import('@testing-library/react')
const { CodexRadarPanel } = await import('../codex-radar-panel')
const { CodexRadarMetricCard } = await import('../codex-radar-metric-card')
const i18n = createInstance()
await i18n.init({ lng: 'en', fallbackLng: 'en', resources: { en, zh } })
afterEach(cleanup)

function metric(overrides: Partial<CodexRadarMetric> = {}): CodexRadarMetric {
  return {
    key: 'gpt_6_astra_ultra',
    label: 'GPT-6 Astra ultra',
    model: 'gpt-6-astra',
    family: 'gpt-6-astra',
    reasoning_effort: 'ultra',
    score: 112.6,
    score_max: 150,
    status: 'provisional',
    coverage: 59,
    tasks: 64,
    graded_runs_count: 71,
    insufficient_data: false,
    average_cost_usd: 2.3727,
    average_task_seconds: 617.07,
    ...overrides,
  }
}
function renderCard(overrides: Partial<CodexRadarMetric> = {}) {
  return render(
    <I18nextProvider i18n={i18n}>
      <CodexRadarMetricCard
        family='gpt-6-astra'
        familyLabel='GPT-6 Astra'
        effort='ultra'
        metric={metric(overrides)}
      />
    </I18nextProvider>
  )
}

describe('Codex Radar v2 presentation', () => {
  test('shows score without denominator, task coverage and observed averages', () => {
    const view = renderCard()
    assert.ok(view.getByText('112.6'))
    assert.ok(view.getByText('71 graded'))
    assert.ok(view.getByText('59/64 tasks covered'))
    assert.ok(view.getByText('$2.37'))
    assert.ok(view.getByText('10.3 min'))
    assert.ok(!view.container.textContent?.includes('/150'))
  })
  test('does not mistake missing averages for free tasks', () => {
    const view = renderCard({
      average_cost_usd: null,
      average_task_seconds: null,
    })
    assert.equal(view.getAllByText('—').length, 2)
    assert.ok(!view.container.textContent?.includes('$0.00'))
    assert.ok(!view.container.textContent?.includes('0 min'))
  })
  test('uses the benchmark scale only for an unlabelled, bounded score bar', () => {
    let view = renderCard()
    const bar = view.container.querySelector<HTMLElement>(
      '[data-radar-score-bar]'
    )
    assert.ok(bar)
    assert.equal(parseFloat(bar.style.width), (112.6 / 150) * 100)
    assert.equal(bar.parentElement?.getAttribute('aria-hidden'), 'true')
    cleanup()
    view = renderCard({ score: 160 })
    assert.equal(
      view.container.querySelector<HTMLElement>('[data-radar-score-bar]')?.style
        .width,
      '100%'
    )
    cleanup()
    view = renderCard({ score: null })
    assert.equal(view.container.querySelector('[data-radar-score-bar]'), null)
    cleanup()
    view = renderCard({ score_max: 0 })
    assert.equal(view.container.querySelector('[data-radar-score-bar]'), null)
  })
  test('shows zero scores, missing scores and low coverage honestly', () => {
    let view = renderCard({ score: 0 })
    assert.ok(view.getByText('0.0'))
    cleanup()
    view = renderCard({ score: null, coverage: 0, insufficient_data: true })
    assert.ok(view.getByText('No public data'))
    assert.ok(view.getByText('Limited data'))
  })
  test('renders only the four requested families with all 24 efforts', () => {
    const families = [
      ['gpt-6-astra', 'GPT-6 Astra'],
      ['gpt-6.1-sol', 'GPT-6.1 Sol'],
      ['gpt-6-sol', 'GPT-6 Sol'],
      ['gpt-5.6-sol', 'GPT-5.6 Sol'],
    ]
    const metrics = families.flatMap(([family, label]) =>
      ['ultra', 'max', 'xhigh', 'high', 'medium', 'low'].map((effort) =>
        metric({
          family,
          model: family,
          label: `${label} ${effort}`,
          reasoning_effort: effort,
        })
      )
    )
    const data: CodexRadarOverview = {
      schema_version: 'radar-bench-v2-150',
      benchmark: 'test',
      fetched_at: '2026-10-14T00:00:00Z',
      updated_at: '2026-10-13T00:00:00Z',
      source_url: 'https://codexradar.com/',
      attribution: '',
      stale: false,
      metrics,
    }
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(
      ['dashboard', 'codex-radar', 'overview', 'radar-bench-v2'],
      data
    )
    const view = render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <CodexRadarPanel />
        </QueryClientProvider>
      </I18nextProvider>
    )
    assert.equal(view.getAllByRole('article').length, 24)
    for (const [, label] of families) {
      assert.equal(
        within(view.getByRole('region', { name: label })).getAllByRole(
          'article'
        ).length,
        6
      )
    }
    for (const [family, label] of families) {
      const region = view.getByRole('region', { name: label })
      assert.equal(region.getAttribute('data-family'), family)
      for (const card of within(region).getAllByRole('article')) {
        assert.equal(card.getAttribute('data-family'), family)
      }
    }
    assert.ok(
      !/luna|terra|DeepSeek|GPT-5\.5|\/150/i.test(
        view.container.textContent ?? ''
      )
    )
    cleanup()
    client.clear()
  })
  test('Chinese card uses the same display contract', async () => {
    await i18n.changeLanguage('zh')
    const view = renderCard({ coverage: 30, insufficient_data: true })
    assert.ok(view.getByText('71 题次'))
    assert.ok(view.getByText('覆盖 30/64 题'))
    assert.ok(view.getByText('数据不足'))
    assert.ok(!view.container.textContent?.includes('/150'))
    cleanup()
    await i18n.changeLanguage('en')
  })
  test('shows automatic recovery inline without repeated global error toasts', async () => {
    const oldAdapter = api.defaults.adapter
    const client = new QueryClient()
    let requests = 0
    api.defaults.adapter = async (config) => {
      requests++
      assert.equal(
        (config as unknown as Record<string, unknown>).skipErrorHandler,
        true
      )
      throw new AxiosError('Radar timeout', 'ERR_BAD_RESPONSE', config, null, {
        status: 502,
        statusText: 'Bad Gateway',
        config,
        headers: { 'retry-after': '60' },
        data: { success: false },
      })
    }
    try {
      const view = render(
        <I18nextProvider i18n={i18n}>
          <QueryClientProvider client={client}>
            <CodexRadarPanel />
          </QueryClientProvider>
        </I18nextProvider>
      )
      await waitFor(() =>
        assert.ok(
          view.getByText('Retrying automatically. No need to refresh the page')
        )
      )
      assert.ok(view.getByText('Codex Radar data is temporarily unavailable'))
      assert.equal(
        view
          .getByRole('button', { name: 'Try again' })
          .hasAttribute('disabled'),
        true
      )
      assert.equal(requests, 1)
    } finally {
      cleanup()
      client.clear()
      api.defaults.adapter = oldAdapter
    }
  })
  test('shows the original sync time and cached warning with usable cards', () => {
    const client = new QueryClient()
    const data: CodexRadarOverview = {
      schema_version: 'radar-bench-v2-150',
      benchmark: 'test',
      fetched_at: '2026-10-14T00:00:00Z',
      updated_at: '2026-10-13T00:00:00Z',
      source_url: 'https://codexradar.com/',
      attribution: '',
      stale: true,
      metrics: [metric()],
    }
    client.setQueryData(
      ['dashboard', 'codex-radar', 'overview', 'radar-bench-v2'],
      data
    )
    const view = render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <CodexRadarPanel />
        </QueryClientProvider>
      </I18nextProvider>
    )
    assert.ok(view.getByText('112.6'))
    assert.ok(view.getByText('· cached data'))
    assert.ok(
      view.getByText(
        'Live sync is temporarily unavailable. Showing the last complete snapshot and retrying automatically'
      )
    )
    assert.ok(view.getByText(/Synced 10(?:\/|&#x2F;)14/))
    assert.equal(view.getAllByRole('article').length, 24)
    cleanup()
    client.clear()
  })
})
