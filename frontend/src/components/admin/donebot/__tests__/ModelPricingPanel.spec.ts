import { config, mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import en from '@/i18n/locales/en/admin/overview'
import zh from '@/i18n/locales/zh/admin/overview'
import LlmModelsPanel from '../LlmModelsPanel.vue'
import ImageModelsPanel from '../ImageModelsPanel.vue'
import AudioModelsPanel from '../AudioModelsPanel.vue'
import VideoModelsPanel from '../VideoModelsPanel.vue'
import donebot, { type DoneBotModel, type ModelsResult } from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { getModels: vi.fn(), models: vi.fn(), pricing: vi.fn(), saveConfiguration: vi.fn(), sync: vi.fn() } }))
function fixture(models: DoneBotModel[]): ModelsResult {
  return { models, rate_multiplier: 1.5, image_price_1k: null, image_price_2k: null, image_price_4k: null, image_rate_multiplier: 1, image_rate_independent: false, image_count_billing: true }
}
function row(model: string, kind: DoneBotModel['kind'], pricing: DoneBotModel['editable_pricing']): DoneBotModel {
  return { model, kind, platform: 'openai', enabled: true, available: true, effective_input_price: null, effective_output_price: null, editable_pricing: pricing }
}
function mockResult(result: ModelsResult) {
  for (const fn of [donebot.getModels, donebot.models, donebot.pricing, donebot.saveConfiguration, donebot.sync]) vi.mocked(fn).mockResolvedValue(structuredClone(result))
}
const originalPlugins = config.global.plugins
beforeEach(() => {
  vi.clearAllMocks(); setActivePinia(createPinia())
  config.global.plugins = [createI18n({ legacy: false, locale: 'zh', messages: { zh: { admin: zh } } })]
})
afterEach(() => { config.global.plugins = originalPlugins })
describe('DoneBot individual model sale prices', () => {
  it('edits only a multiplier for each LLM and never submits token prices', async () => {
    mockResult(fixture([
      row('gpt-test', 'llm', { rate_multiplier: 1.2 }),
      row('claude-test', 'llm', { rate_multiplier: 0 })
    ]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.findAll('input[type=number]')).toHaveLength(2)
    expect(wrapper.find('[data-testid=price-gpt-test-input_price]').exists()).toBe(false)
    expect(wrapper.text()).toContain('渠道价格 × 每模型倍率')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-gpt-test-rate_multiplier]').element.value).toBe('1.2')
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-claude-test-rate_multiplier]').element.value).toBe('0')
    await wrapper.get('[data-testid=price-gpt-test-rate_multiplier]').setValue('1.5')
    await wrapper.get('[data-test=save-configuration]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('llm', { models: [
      { model: 'gpt-test', enabled: true, pricing: { rate_multiplier: 1.5 } },
      { model: 'claude-test', enabled: true, pricing: { rate_multiplier: 0 } }
    ] })
  })
  it('defaults missing LLM multipliers to one and never creates extra catalog rows', async () => {
    mockResult(fixture([row('account-enabled-model', 'llm', {})]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get<HTMLInputElement>('[data-testid=price-account-enabled-model-rate_multiplier]').element.value).toBe('1')
  })
  it('edits separate image model prices without submitting shared group image overrides', async () => {
    mockResult(fixture([
      { ...row('seedream-5-0-flash', 'images', { image_prices: { '1K': .045, '2K': .09 } }), image_resolutions: ['1K', '1.5K', '2K'] },
      row('gpt-image-1', 'images', { image_prices: { '1K': .1, '2K': .2, '4K': .3 } })
    ]))
    const wrapper = mount(ImageModelsPanel, { global: { stubs: { ImageTaskLookup: true } } }); await flushPromises()
    expect(wrapper.find('[data-testid="price-seedream-5-0-flash-image-4K"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('1K / 1.5K')
    expect(wrapper.find('[data-testid="price-gpt-image-1-image-4K"]').exists()).toBe(true)
    await wrapper.get('[data-testid="price-seedream-5-0-flash-image-1K"]').setValue('0.055')
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('images', { models: [
      { model: 'seedream-5-0-flash', enabled: true, pricing: { image_prices: { '1K': .055, '2K': .09 } } },
      { model: 'gpt-image-1', enabled: true, pricing: { image_prices: { '1K': .1, '2K': .2, '4K': .3 } } }
    ] })
  })
  it('saves Seedream 5.5 reference input prices separately from output tiers', async () => {
    mockResult(fixture([{ ...row('seedream-5-5', 'images', { image_prices: { '1K': .04, '2K': .08, input: .01 } }),
      image_resolutions: ['1K', '1.5K', '2K'], image_input_billing: true }]))
    const wrapper = mount(ImageModelsPanel, { global: { stubs: { ImageTaskLookup: true } } }); await flushPromises()
    expect(wrapper.text()).toContain('输入参考图（每张）')
    expect(wrapper.find('[data-testid="price-seedream-5-5-image-4K"]').exists()).toBe(false)
    await wrapper.get('[data-testid="price-seedream-5-5-image-input"]').setValue('0.02')
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('images', { models: [
      { model: 'seedream-5-5', enabled: true, pricing: { image_prices: { '1K': .04, '2K': .08, input: .02 } } }
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
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('audio', { models: [
      { model: 'my-music', enabled: true, pricing: { tier_prices: { v6: .12, 'v6-wild': null, 'v6-mini': .08, 'custom-model': null } } },
      { model: 'my-speech', enabled: true, pricing: { tier_prices: { tts: 12 } } }
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
    expect(wrapper.text()).toContain('USD / 输出秒')
    expect(wrapper.find('[data-testid*="1080p"]').exists()).toBe(false)
    await wrapper.get('[data-testid="price-doubao-seedance-2-0-fast-260128-video-720p-reference"]').setValue('0.12')
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('video', { models: [{ model: 'doubao-seedance-2-0-fast-260128', enabled: true, pricing: { video_prices: [
      { resolution: '480p', reference_video: false, price_per_second: .1 },
      { resolution: '480p', reference_video: true, price_per_second: .05 },
      { resolution: '720p', reference_video: false, price_per_second: .2 },
      { resolution: '720p', reference_video: true, price_per_second: .12 }
    ] } }] })
  })
  it.each([
    {
      locale: 'zh',
      resolution: '分辨率',
      outputUnit: '无参考视频（USD / 输出秒）',
      referenceUnit: '有参考视频（USD / 输入视频秒 + 输出秒）',
      measurement: '无参考视频仅按实际输出时长计费；有参考视频按全部输入视频总时长 + 实际输出时长计费。',
      sharedRate: '多个输入视频的时长相加，输入与输出均使用同一有参考视频秒价。'
    },
    {
      locale: 'en',
      resolution: 'Resolution',
      outputUnit: 'No reference video (USD / output second)',
      referenceUnit: 'With reference video (USD / input video second + output second)',
      measurement: 'Without reference video, only actual output duration is billed. With reference video, total input video duration + actual output duration is billed.',
      sharedRate: 'Durations of multiple input videos are summed; input and output use the same reference-video rate.'
    }
  ])('explains reference video billing duration in $locale', async ({ locale, resolution, outputUnit, referenceUnit, measurement, sharedRate }) => {
    mockResult(fixture([{ ...row('doubao-seedance-2-5-260628', 'video', {}), video_resolutions: ['720p'] }]))
    const i18n = createI18n({ legacy: false, locale, messages: { en: { admin: en }, zh: { admin: zh } } })
    config.global.plugins = [i18n]
    const wrapper = mount(VideoModelsPanel); await flushPromises()
    expect(wrapper.findAll('th').map(header => header.text())).toEqual([resolution, outputUnit, referenceUnit])
    expect(wrapper.text()).toContain(measurement)
    expect(wrapper.text()).toContain(sharedRate)
    wrapper.unmount()
  })
  it('requires channel pricing before enabling an account LLM', async () => {
    mockResult(fixture([{ ...row('gpt-new', 'llm', { rate_multiplier: 1 }), enabled: false, pricing_configured: false }]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 gpt-new"]').element.disabled).toBe(true)
    expect(wrapper.text()).toContain('请先配置渠道价格')
    expect(wrapper.text()).not.toContain('请先保存模型价格')
    await wrapper.get('[data-testid=price-gpt-new-rate_multiplier]').setValue('0')
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 gpt-new"]').element.disabled).toBe(true)
  })
  it('can price and enable a new media model in the same save', async () => {
    mockResult(fixture([{ ...row('seedream-5-0-flash', 'images', { image_prices: {} }), enabled: false, pricing_configured: false, image_resolutions: ['1K', '1.5K', '2K'] }]))
    const wrapper = mount(ImageModelsPanel, { global: { stubs: { ImageTaskLookup: true } } }); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 seedream-5-0-flash"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="price-seedream-5-0-flash-image-2K"]').setValue('0.1')
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 seedream-5-0-flash"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="price-seedream-5-0-flash-image-2K"]').setValue('')
    await wrapper.get('[data-testid="price-seedream-5-0-flash-image-1K"]').setValue('0.05')
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 seedream-5-0-flash"]').element.disabled).toBe(false)
    await wrapper.get('[aria-label="启用 seedream-5-0-flash"]').setValue(true)
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledTimes(1)
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('images', { models: [{ model: 'seedream-5-0-flash', enabled: true, pricing: { image_prices: { '1K': 0.05, '2K': null } } }] })
  })
  it('saves model switches and current multipliers in one request', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { rate_multiplier: 1.2 })]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    expect(wrapper.find('[data-test="save-models"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="save-pricing"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="save-configuration"]').text()).toBe('保存')
    await wrapper.get('[data-testid="price-gpt-test-rate_multiplier"]').setValue('7')
    await wrapper.get('[aria-label="启用 gpt-test"]').setValue(false)
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).toHaveBeenCalledTimes(1)
    expect(donebot.saveConfiguration).toHaveBeenCalledWith('llm', { models: [{ model: 'gpt-test', enabled: false, pricing: { rate_multiplier: 7 } }] })
    expect(donebot.models).not.toHaveBeenCalled()
    expect(donebot.pricing).not.toHaveBeenCalled()
  })
  it('keeps edited switches and multipliers together when saving fails', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { rate_multiplier: 1.2 })]))
    vi.mocked(donebot.saveConfiguration).mockRejectedValueOnce(new Error('保存失败'))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    await wrapper.get('[data-testid="price-gpt-test-rate_multiplier"]').setValue('7')
    await wrapper.get('[aria-label="启用 gpt-test"]').setValue(false)
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="price-gpt-test-rate_multiplier"]').element.value).toBe('7')
    expect(wrapper.get<HTMLInputElement>('[aria-label="启用 gpt-test"]').element.checked).toBe(false)
    expect(wrapper.text()).toContain('保存失败')
    expect(donebot.saveConfiguration).toHaveBeenCalledTimes(1)
    expect(donebot.models).not.toHaveBeenCalled()
    expect(donebot.pricing).not.toHaveBeenCalled()
  })
  it('blocks invalid multipliers from saving switches or pricing', async () => {
    mockResult(fixture([row('gpt-test', 'llm', { rate_multiplier: 1.2 })]))
    const wrapper = mount(LlmModelsPanel); await flushPromises()
    for (const value of ['-1', '', '1e309']) {
      await wrapper.get('[data-testid="price-gpt-test-rate_multiplier"]').setValue(value)
      expect(wrapper.get<HTMLButtonElement>('[data-test="save-configuration"]').element.disabled).toBe(true)
    }
    await wrapper.get('[aria-label="启用 gpt-test"]').setValue(false)
    await wrapper.get('[data-test="save-configuration"]').trigger('click'); await flushPromises()
    expect(donebot.saveConfiguration).not.toHaveBeenCalled()
    expect(donebot.models).not.toHaveBeenCalled()
    expect(donebot.pricing).not.toHaveBeenCalled()
  })
  it.each([
    [ImageModelsPanel, '添加并启用图片账号'],
    [AudioModelsPanel, '添加并启用 APIMart 音频账号'],
    [VideoModelsPanel, '添加并启用视频账号']
  ])('guides empty media catalogs to their account type', async (component, message) => {
    mockResult(fixture([]))
    const wrapper = mount(component, { global: { stubs: { ImageTaskLookup: true } } }); await flushPromises()
    expect(wrapper.text()).toContain(message)
  })
})
