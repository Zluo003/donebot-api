import { describe, expect, it } from 'vitest'
import { shouldShowAPIKeyManagement } from '../donebot'

describe('DoneBot API key management visibility', () => {
  it('hides ordinary API key management only when DoneBot mode is explicitly enabled', () => {
    expect(shouldShowAPIKeyManagement({ donebot_enabled: true })).toBe(false)
    expect(shouldShowAPIKeyManagement({ donebot_enabled: false })).toBe(true)
    expect(shouldShowAPIKeyManagement(undefined)).toBe(true)
  })
})
