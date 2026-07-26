import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { Channel } from '../../types'
import { getChannelTableRowId, type TagRow } from '../channel-utils'

function channel(id: number): Channel {
  return { id } as Channel
}

describe('channel table row identity', () => {
  test('keeps each channel identity when priority updates reorder the rows', () => {
    const first = channel(101)
    const updated = channel(202)
    const third = channel(303)

    const beforeUpdate = [first, updated, third].map(getChannelTableRowId)
    const afterUpdate = [updated, first, third].map(getChannelTableRowId)

    assert.deepEqual(beforeUpdate, [
      'channel:101',
      'channel:202',
      'channel:303',
    ])
    assert.deepEqual(afterUpdate, ['channel:202', 'channel:101', 'channel:303'])
  })

  test('uses separate namespaces for tag and channel rows', () => {
    const tagRow = {
      id: '202' as unknown as number,
      tag: '202',
      children: [channel(202)],
    } as TagRow

    assert.equal(getChannelTableRowId(tagRow), 'tag:202')
    assert.equal(getChannelTableRowId(channel(202)), 'channel:202')
  })
})
