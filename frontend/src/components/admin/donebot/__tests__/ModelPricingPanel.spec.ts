import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import LlmModelsPanel from '../LlmModelsPanel.vue'
import ImageModelsPanel from '../ImageModelsPanel.vue'
import AudioModelsPanel from '../AudioModelsPanel.vue'
import VideoModelsPanel from '../VideoModelsPanel.vue'
import donebot, { type DoneBotModel, type ModelsResult } from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { getModels: vi.fn(), models: vi.fn(), pricing: vi.fn(), sync: vi.fn() } }))
function fixture(models: DoneBotModel[]): ModelsResult {
  return { models, rate_multiplier: 1.5, image_price_1k: null, image_price_2k: null, image_price_4k: null, image_rate_multiplier: 1, image_rate_independent: false, image_count_billing: true }
}
function row(model: string, kind: DoneBotModel['kind'], pricing: DoneBotModel['editable_pricing']): DoneBotModel {
  return { model, kind, platform: 'openai', enabled: true, available: true, effective_input_price: null, effective_output_price: null, editable_pricing: pricing }
}
function mockResult(result: ModelsResult) {
  for (const fn of [donebot.getModels, donebot.models, donebot.pricing, donebot.sync]) vi.mocked(fn).mockResolvedValue(structuredClone(result))
}
beforeEach(() => { vi.clearAllMocks(); setActivePinia(createPinia()) })
describe('DoneBot individual model sale prices', () => {
  it('edits input, output and cache token prices per model in USD per million tokens', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { input_price: .000003, output_price: .000004, cache_write_price: .000002, cache_read_price: .000001 })]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-gpt-test-input_price"]').element.value).toBe('3')
    expect(wrapper.text()).not.toContain('保存倍率')
    await wrapper.get('[data-testid="price-gpt-test-input_price"]').setValue('6')
    await wrapper.get('[data-testid="price-gpt-test-cache_read_price"]').setValue('0.5')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('llm', { models: [{ model: 'gpt-test', pricing: { input_price: .000006, output_price: .000004, cache_write_price: .000002, cache_read_price: .0000005 } }] })
  })
  it('edits separate image model prices without submitting shared group image overrides', async () => {
    mockResult(fixture([
      row('seedream-5-0-flash', 'images', { image_prices: { '1K': .045, '2K': .09, '4K': null } }),
      row('gpt-image-1', 'images', { image_prices: { '1K': .1, '2K': .2, '4K': .3 } })
    ]))
    const wrapper = mount(ImageModelsPanel, { global: { stubs: { ImageTaskLookup: true } } }); await flushPromises()
    await wrapper.get('[data-testid="price-seedream-5-0-flash-image-1K"]').setValue('0.055')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('images', { models: [
      { model: 'seedream-5-0-flash', pricing: { image_prices: { '1K': .055, '2K': .09, '4K': null } } },
      { model: 'gpt-image-1', pricing: { image_prices: { '1K': .1, '2K': .2, '4K': .3 } } }
    ] })
  })
  it('edits Suno version and TTS character prices per model with backend pricing units', async () => {
    const music = row('my-music', 'audio', { tier_prices: { v6: .12, 'v6-mini': .06 } })
    const speech = { ...row('my-speech', 'audio', { tier_prices: { tts: 15 } }), audio_price_unit: 'million_characters' as const }
    mockResult(fixture([music, speech]))
    const wrapper = mount(AudioModelsPanel); await flushPromises()
    expect(wrapper.text()).toContain('百万字符')
    await wrapper.get('[data-testid="price-my-music-tier-v6-mini"]').setValue('0.08')
    await wrapper.get('[data-testid="price-my-speech-tier-tts"]').setValue('12')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('audio', { models: [
      { model: 'my-music', pricing: { tier_prices: { v6: .12, 'v6-wild': null, 'v6-mini': .08, 'custom-model': null } } },
      { model: 'my-speech', pricing: { tier_prices: { tts: 12 } } }
    ] })
  })
  it('edits per-second video prices by official resolution and reference video presence', async () => {
    mockResult(fixture([{ ...row('doubao-seedance-2-0-fast-260128', 'video', { video_prices: [
      { resolution: '480p', reference_video: false, price_per_second: .1 },
      { resolution: '480p', reference_video: true, price_per_second: .05 },
      { resolution: '720p', reference_video: false, price_per_second: .2 },
      { resolution: '720p', reference_video: true, price_per_second: .1 }
    ] }), video_resolutions: ['480p', '720p'] }]))
    const wrapper = mount(VideoModelsPanel); await flushPromises()
    expect(wrapper.text()).toContain('USD / 秒')
    expect(wrapper.find('[data-testid*="1080p"]').exists()).toBe(false)
    await wrapper.get('[data-testid="price-doubao-seedance-2-0-fast-260128-video-720p-reference"]').setValue('0.12')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('video', { models: [{ model: 'doubao-seedance-2-0-fast-260128', pricing: { video_prices: [
      { resolution: '480p', reference_video: false, price_per_second: .1 },
      { resolution: '480p', reference_video: true, price_per_second: .05 },
      { resolution: '720p', reference_video: false, price_per_second: .2 },
      { resolution: '720p', reference_video: true, price_per_second: .12 }
    ] } }] })
  })
  it('lets an unpriced account model receive its first price before it can be enabled', async () => {
    mockResult(fixture([{ ...row('gpt-new', 'llm', {}), enabled: false, pricing_configured: false }]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 gpt-new"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-gpt-new-input_price"]').element.disabled).toBe(false)
    expect(wrapper.text()).toContain('请先保存模型价格')
    await wrapper.get('[data-testid="price-gpt-new-input_price"]').setValue('2')
    await wrapper.get('[data-test="save-pricing"]').trigger('click'); await flushPromises()
    expect(donebot.pricing).toHaveBeenCalledWith('llm', { models: [{ model: 'gpt-new', pricing: { input_price: .000002, output_price: null, cache_write_price: null, cache_read_price: null } }] })
  })
  it('keeps unsaved model prices when saving model switches', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { input_price: .000003 })]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    await wrapper.get('[data-testid="price-gpt-test-input_price"]').setValue('7')
    await wrapper.get('[aria-label="启用 gpt-test"]').setValue(false)
    await wrapper.get('[data-test="save-models"]').trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-gpt-test-input_price"]').element.value).toBe('7')
  })
  it('blocks invalid prices and keeps model toggles separate from pricing writes', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { input_price: .000003 })]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    await wrapper.get('[data-testid="price-gpt-test-input_price"]').setValue('-1')
    expect(wrapper.get<HTMLButtonElement>('[data-test="save-pricing"]').element.disabled).toBe(true)
    await wrapper.get('[aria-label="启用 gpt-test"]').setValue(false)
    await wrapper.get('[data-test="save-models"]').trigger('click'); await flushPromises()
    expect(donebot.models).toHaveBeenCalledWith('llm', [{ model: 'gpt-test', enabled: false }])
    expect(donebot.pricing).not.toHaveBeenCalled()
  })
})
