import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AudioModelsPanel from '../AudioModelsPanel.vue'
import donebot from '@/api/admin/donebot'
import type { ModelsResult } from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { getModels: vi.fn(), models: vi.fn(), pricing: vi.fn(), sync: vi.fn() } }))
const fixture: ModelsResult = {
  models: [
    { model: 'suno', platform: 'openai', enabled: true, available: true, kind: 'audio', effective_input_price: null, effective_output_price: null, effective_per_request_price: 0.12, effective_tier_prices: { v6: 0.12, 'v6-mini': 0.06 } },
    { model: 'gpt-4o-mini-tts', platform: 'openai', enabled: true, available: true, kind: 'audio', effective_input_price: null, effective_output_price: null, effective_per_request_price: 15, effective_tier_prices: { tts: 15 } }
  ], rate_multiplier: 1.5, image_price_1k: null, image_price_2k: null, image_price_4k: null, image_rate_multiplier: 1, image_rate_independent: false, image_count_billing: false
}
beforeEach(() => {
  vi.clearAllMocks(); setActivePinia(createPinia())
  for (const fn of [donebot.getModels, donebot.models, donebot.pricing, donebot.sync]) vi.mocked(fn).mockResolvedValue(structuredClone(fixture))
})
describe('Audio model management', () => {
  it('displays server prices with the correct Suno and TTS units and saves switches', async () => {
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    expect(donebot.getModels).toHaveBeenCalledWith('audio')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-suno-tier-v6]').element.value).toBe('0.12')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-suno-tier-v6-mini]').element.value).toBe('0.06')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-gpt-4o-mini-tts-tier-tts]').element.value).toBe('15')
    expect(wrapper.text()).toContain('百万字符')
    expect(wrapper.text()).toContain('Max')
    await wrapper.get('[aria-label="启用 suno"]').setValue(false)
    await wrapper.get('[data-test="save-models"]').trigger('click'); await flushPromises()
    expect(donebot.models).toHaveBeenCalledWith('audio', [{ model: 'suno', enabled: false }, { model: 'gpt-4o-mini-tts', enabled: true }])
  })
  it('edits per-model prices and syncs only audio models', async () => {
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    await wrapper.get('[data-testid=price-suno-tier-v6]').setValue('0.2')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('audio', expect.objectContaining({ models: expect.arrayContaining([{ model: 'suno', pricing: expect.objectContaining({ tier_prices: expect.objectContaining({ v6: 0.2 }) }) }]) }))
    await wrapper.get('[data-test="sync-models"]').trigger('click'); await flushPromises()
    expect(donebot.sync).toHaveBeenCalledWith('audio')
  })
})
