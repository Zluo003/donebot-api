import { config, flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh/admin/overview'
import AudioModelsPanel from '../AudioModelsPanel.vue'
import donebot from '@/api/admin/donebot'
import type { ModelsResult } from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { getModels: vi.fn(), models: vi.fn(), pricing: vi.fn(), saveConfiguration: vi.fn(), sync: vi.fn() } }))
const fixture: ModelsResult = {
  models: [
    { model: 'suno', platform: 'openai', enabled: true, available: true, kind: 'audio', audio_price_unit: 'request', effective_input_price: null, effective_output_price: null, effective_per_request_price: 0.12, effective_tier_prices: { v6: 0.12, 'v6-mini': 0.06 } },
    { model: 'gpt-4o-mini-tts', platform: 'openai', enabled: true, available: true, kind: 'audio', audio_price_unit: 'million_characters', effective_input_price: null, effective_output_price: null, effective_per_request_price: 15, effective_tier_prices: { tts: 15 } },
    { model: 'qwen-audio-3.1-tts-flash', platform: 'openai', enabled: true, available: true, kind: 'audio', audio_price_unit: 'million_tokens', effective_input_price: 0.00001, effective_output_price: 0.00012, effective_per_request_price: null },
    { model: 'qwen-audio-3.1-asr-flash-streaming', platform: 'openai', enabled: true, available: true, kind: 'audio', audio_price_unit: 'million_tokens', effective_input_price: 0.000002, effective_output_price: 0.000008, effective_per_request_price: null }
  ], rate_multiplier: 1.5, image_price_1k: null, image_price_2k: null, image_price_4k: null, image_rate_multiplier: 1, image_rate_independent: false, image_count_billing: false
}
const originalPlugins = config.global.plugins
beforeEach(() => {
  vi.clearAllMocks(); setActivePinia(createPinia())
  config.global.plugins = [createI18n({ legacy: false, locale: 'zh', messages: { zh: { admin: zh } } })]
  for (const fn of [donebot.getModels, donebot.models, donebot.pricing, donebot.saveConfiguration, donebot.sync]) vi.mocked(fn).mockResolvedValue(structuredClone(fixture))
})
afterEach(() => { config.global.plugins = originalPlugins })
describe('Audio model management', () => {
  it('displays server prices with the correct Suno and TTS units and saves switches', async () => {
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    expect(donebot.getModels).toHaveBeenCalledWith('audio')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-suno-tier-v6]').element.value).toBe('0.12')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-suno-tier-v6-mini]').element.value).toBe('0.06')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-gpt-4o-mini-tts-tier-tts]').element.value).toBe('15')
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-qwen-audio-3.1-tts-flash-token-input"]').element.value).toBe('10')
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-qwen-audio-3.1-tts-flash-token-output"]').element.value).toBe('120')
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-qwen-audio-3.1-asr-flash-streaming-token-input"]').element.value).toBe('2')
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-qwen-audio-3.1-asr-flash-streaming-token-output"]').element.value).toBe('8')
    expect(wrapper.text()).toContain('百万字符')
    expect(wrapper.text()).toContain('USD / 百万 Token')
    expect(wrapper.text()).not.toContain('CNY / 百万 Token')
    expect(wrapper.text()).toContain('Max')
    await wrapper.get('[aria-label="启用 suno"]').setValue(false)
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('audio', {
      models: expect.arrayContaining([
        expect.objectContaining({ model: 'suno', enabled: false, pricing: expect.objectContaining({ tier_prices: expect.objectContaining({ v6: 0.12 }) }) }),
        expect.objectContaining({ model: 'gpt-4o-mini-tts', enabled: true }),
        expect.objectContaining({ model: 'qwen-audio-3.1-tts-flash', enabled: true, pricing: { input_price: 0.00001, output_price: 0.00012 } }),
        expect.objectContaining({ model: 'qwen-audio-3.1-asr-flash-streaming', enabled: true, pricing: { input_price: 0.000002, output_price: 0.000008 } })
      ])
    })
  })
  it('edits per-model prices and syncs only audio models', async () => {
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    await wrapper.get('[data-testid=price-suno-tier-v6]').setValue('0.2')
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('audio', expect.objectContaining({ models: expect.arrayContaining([{ model: 'suno', enabled: true, pricing: expect.objectContaining({ tier_prices: expect.objectContaining({ v6: 0.2 }) }) }]) }))
    await wrapper.get('[data-test="sync-models"]').trigger('click'); await flushPromises()
    expect(donebot.sync).toHaveBeenCalledWith('audio')
  })
  it('requires both input and output prices before enabling an unpriced million_tokens audio model', async () => {
    const unpriced: ModelsResult = {
      ...structuredClone(fixture),
      models: [
        { model: 'qwen-audio-3.1-asr-flash-streaming', platform: 'openai', enabled: false, available: true, kind: 'audio', pricing_configured: false, audio_price_unit: 'million_tokens', effective_input_price: null, effective_output_price: null, effective_per_request_price: null }
      ]
    }
    vi.mocked(donebot.getModels).mockResolvedValueOnce(unpriced)
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    const toggle = wrapper.get<HTMLInputElement>('[aria-label="启用 qwen-audio-3.1-asr-flash-streaming"]')
    expect(toggle.element.disabled).toBe(true)
    await wrapper.get('[data-testid="price-qwen-audio-3.1-asr-flash-streaming-token-input"]').setValue('2')
    expect(toggle.element.disabled).toBe(true)
    await wrapper.get('[data-testid="price-qwen-audio-3.1-asr-flash-streaming-token-output"]').setValue('8')
    expect(toggle.element.disabled).toBe(false)
  })
})
