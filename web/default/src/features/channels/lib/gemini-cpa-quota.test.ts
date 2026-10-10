import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import type { Channel } from '../types'
import {
  getGeminiCPAAccountQuotaPools,
  getGeminiCPAQuotaPoolSummary,
  isGeminiCPAChannel,
  parseGeminiCPAQuotaMeta,
  type GeminiCPAQuotaMeta,
} from './gemini-cpa-quota'

describe('Gemini CPA quota', () => {
  const channel = {
    type: 1,
    base_url: 'http://cliproxy-api:8317',
    models: 'gpt-6-sol',
    model_mapping: '{"gpt-6-sol":"gemini-3.8-flash-tiered"}',
  } as Channel

  test('detects mapped Gemini CPA but not Kimi or official Gemini', () => {
    assert.equal(isGeminiCPAChannel(channel), true)
    assert.equal(
      isGeminiCPAChannel({
        ...channel,
        base_url: 'https://generativelanguage.googleapis.com',
      }),
      false
    )
    assert.equal(
      isGeminiCPAChannel({
        ...channel,
        model_mapping: '{"gpt-6-sol":"kimi-k3"}',
      }),
      false
    )
    assert.equal(isGeminiCPAChannel({ ...channel, type: 24 }), false)
    assert.equal(
      isGeminiCPAChannel({
        ...channel,
        models: 'gemini-pro',
        model_mapping: 'invalid',
      }),
      true
    )
  })

  test('preserves zero, unknown and independent account reset times', () => {
    const meta = parseGeminiCPAQuotaMeta(
      JSON.stringify({
        gemini_cpa_quota: {
          remaining_percent: 84,
          account_count: 2,
          available_account_count: 2,
          accounts: [
            {
              id: 'one',
              remaining_percent: 70,
              tier: 'g1-pro-tier',
              models: [
                {
                  model: 'gemini-pro',
                  remaining_percent: 70,
                  reset_at: 1791559391,
                },
                {
                  model: 'claude-sonnet',
                  remaining_percent: 0,
                  reset_at: 1791876889,
                },
                { model: 'not-provided', remaining_percent: null },
              ],
            },
            {
              id: 'two',
              remaining_percent: 98,
              models: [
                {
                  model: 'gemini-pro',
                  remaining_percent: 98,
                  reset_at: 1791566021,
                },
              ],
            },
          ],
        },
      })
    )
    assert.equal(meta?.remainingPercent, 84)
    assert.equal(meta?.accounts.length, 2)
    assert.equal(meta?.accounts[0].models[1].remainingPercent, 0)
    assert.equal(meta?.accounts[0].models[2].remainingPercent, null)
    assert.notEqual(
      meta?.accounts[0].models[0].resetAt,
      meta?.accounts[1].models[0].resetAt
    )
  })

  test('rejects invalid snapshots and does not substitute channel dollar balance', () => {
    assert.equal(parseGeminiCPAQuotaMeta('invalid'), null)
    assert.equal(parseGeminiCPAQuotaMeta('{"balance":99}'), null)
    const meta = parseGeminiCPAQuotaMeta(
      '{"gemini_cpa_quota":{"remaining_percent":120,"error":"HTTP 429","partial":true}}'
    )
    assert.equal(meta?.remainingPercent, null)
    assert.equal(meta?.partial, true)
    assert.equal(meta?.error, 'HTTP 429')
  })

  test('collapses duplicate aliases and keeps exhausted independent pools', () => {
    const pools = getGeminiCPAAccountQuotaPools([
      { model: 'gemini-pro', remainingPercent: 90, resetAt: 200 },
      { model: 'gemini-pro-agent', remainingPercent: 90, resetAt: 200 },
      { model: 'gemini-flash', remainingPercent: 70, resetAt: 200 },
      { model: 'gemini-not-returned', remainingPercent: null, resetAt: null },
      { model: 'claude-sonnet', remainingPercent: 0, resetAt: 500 },
      { model: 'gpt-oss-120b', remainingPercent: 0, resetAt: 500 },
      { model: 'unrelated-model', remainingPercent: 99, resetAt: 100 },
    ])
    assert.deepEqual(pools, [
      {
        name: 'gemini',
        remainingPercent: 70,
        resetAt: 200,
        resetVaries: false,
      },
      { name: 'claude', remainingPercent: 0, resetAt: 500, resetVaries: false },
      {
        name: 'gpt-oss',
        remainingPercent: 0,
        resetAt: 500,
        resetVaries: false,
      },
    ])
    assert.equal(
      pools.some((pool) => 'model' in pool),
      false
    )
    assert.equal(
      pools.some((pool) => /5h|month/.test(pool.name)),
      false
    )
  })

  test('keeps wholly unknown quotas unknown instead of showing zero', () => {
    assert.deepEqual(
      getGeminiCPAAccountQuotaPools([
        { model: 'gemini-not-returned', remainingPercent: null, resetAt: null },
      ]),
      [
        {
          name: 'gemini',
          remainingPercent: null,
          resetAt: null,
          resetVaries: false,
        },
      ]
    )
  })

  test('does not invent a reset for mixed or incomplete model reset times', () => {
    const mixed = getGeminiCPAAccountQuotaPools([
      { model: 'gemini-pro', remainingPercent: 80, resetAt: 200 },
      { model: 'gemini-flash', remainingPercent: 40, resetAt: 300 },
    ])[0]
    assert.equal(mixed.remainingPercent, 40)
    assert.equal(mixed.resetAt, null)
    assert.equal(mixed.resetVaries, true)

    const incomplete = getGeminiCPAAccountQuotaPools([
      { model: 'gemini-pro', remainingPercent: 80, resetAt: 200 },
      { model: 'gemini-flash', remainingPercent: 40, resetAt: null },
    ])[0]
    assert.equal(incomplete.resetAt, null)
  })

  function quotaMeta(accounts: unknown[]): GeminiCPAQuotaMeta {
    const meta = parseGeminiCPAQuotaMeta(
      JSON.stringify({ gemini_cpa_quota: { accounts } })
    )
    assert.ok(meta)
    return meta
  }

  test('averages accounts equally regardless of the number of model aliases', () => {
    const meta = quotaMeta([
      {
        id: 'one',
        models: [
          { model: 'gemini-pro', remaining_percent: 50, reset_at: 200 },
          { model: 'gemini-pro-agent', remaining_percent: 50, reset_at: 200 },
          { model: 'gemini-flash', remaining_percent: 50, reset_at: 200 },
        ],
      },
      {
        id: 'two',
        models: [
          { model: 'gemini-pro', remaining_percent: 100, reset_at: 300 },
        ],
      },
    ])
    assert.deepEqual(getGeminiCPAQuotaPoolSummary(meta), [
      {
        name: 'gemini',
        remainingPercent: 75,
        resetAt: null,
        resetVaries: true,
      },
    ])
  })

  test('ignores failed account values while retaining independent pool rows', () => {
    const meta = quotaMeta([
      {
        id: 'success',
        models: [
          { model: 'gemini-pro', remaining_percent: 50, reset_at: 200 },
          { model: 'claude-sonnet', remaining_percent: 0, reset_at: 500 },
        ],
      },
      {
        id: 'failed',
        error: 'HTTP 429',
        models: [
          { model: 'gemini-pro', remaining_percent: 100, reset_at: 300 },
        ],
      },
    ])
    assert.deepEqual(getGeminiCPAQuotaPoolSummary(meta), [
      {
        name: 'gemini',
        remainingPercent: 50,
        resetAt: 200,
        resetVaries: false,
      },
      { name: 'claude', remainingPercent: 0, resetAt: 500, resetVaries: false },
    ])
  })

  test('keeps per-account reset times and only shares a confirmed common reset', () => {
    const meta = quotaMeta([
      {
        id: 'one',
        models: [{ model: 'gemini-pro', remaining_percent: 40, reset_at: 200 }],
      },
      {
        id: 'two',
        models: [{ model: 'gemini-pro', remaining_percent: 60, reset_at: 200 }],
      },
    ])
    assert.deepEqual(getGeminiCPAQuotaPoolSummary(meta), [
      {
        name: 'gemini',
        remainingPercent: 50,
        resetAt: 200,
        resetVaries: false,
      },
    ])
    assert.equal(meta.accounts[0].models[0].resetAt, 200)
    meta.accounts[1].models[0].resetAt = null
    assert.equal(getGeminiCPAQuotaPoolSummary(meta)[0].resetAt, null)
  })
})
