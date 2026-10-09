import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import type { Channel } from '../types'
import { isGeminiCPAChannel, parseGeminiCPAQuotaMeta } from './gemini-cpa-quota'

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
})
