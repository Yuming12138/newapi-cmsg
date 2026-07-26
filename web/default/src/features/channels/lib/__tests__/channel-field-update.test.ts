import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  CHANNEL_FIELD_UPDATE_DELAY_MS,
  createChannelFieldUpdateScheduler,
} from '../channel-field-update'

function createFakeTimers() {
  const pending = new Map<number, () => void>()
  let nextId = 1

  return {
    timers: {
      setTimeout: (callback: () => void, delay: number) => {
        assert.equal(delay, CHANNEL_FIELD_UPDATE_DELAY_MS)
        const id = nextId++
        pending.set(id, callback)
        return id
      },
      clearTimeout: (id: number) => {
        pending.delete(id)
      },
    },
    fireAll() {
      const callbacks = [...pending.values()]
      pending.clear()
      for (const callback of callbacks) callback()
    },
    get pendingCount() {
      return pending.size
    },
  }
}

describe('channel field update scheduler', () => {
  test('coalesces rapid schedules into one update with the latest value', () => {
    const fake = createFakeTimers()
    const updates: number[] = []
    const scheduler = createChannelFieldUpdateScheduler(
      (value) => updates.push(value),
      fake.timers
    )

    scheduler.schedule(1)
    scheduler.schedule(2)
    scheduler.schedule(3)
    assert.deepEqual(updates, [])
    assert.equal(fake.pendingCount, 1)

    fake.fireAll()
    assert.deepEqual(updates, [3])
  })

  test('flush commits the pending value immediately and cancels the timer', () => {
    const fake = createFakeTimers()
    const updates: number[] = []
    const scheduler = createChannelFieldUpdateScheduler(
      (value) => updates.push(value),
      fake.timers
    )

    scheduler.schedule(7)
    scheduler.flush()
    assert.deepEqual(updates, [7])
    assert.equal(fake.pendingCount, 0)

    fake.fireAll()
    assert.deepEqual(updates, [7])
  })

  test('preserves a pending value of 0', () => {
    const fake = createFakeTimers()
    const updates: number[] = []
    const scheduler = createChannelFieldUpdateScheduler(
      (value) => updates.push(value),
      fake.timers
    )

    scheduler.schedule(0)
    scheduler.flush()
    assert.deepEqual(updates, [0])
  })
})
