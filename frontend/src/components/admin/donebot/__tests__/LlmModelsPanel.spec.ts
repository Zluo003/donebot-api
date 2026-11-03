import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import LlmModelsPanel from '../LlmModelsPanel.vue'
import donebot from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { getModels: vi.fn(), models: vi.fn(), pricing: vi.fn(), saveConfiguration: vi.fn(), sync: vi.fn() } }))
const fixture = { models: [{ model: 'gpt-test', platform: 'openai', enabled: true, available: true, kind: 'llm' as const, effective_input_price: .000003, effective_output_price: .000004 }], rate_multiplier: 1.5, image_price_1k: null, image_price_2k: null, image_price_4k: null, image_rate_multiplier: 1, image_rate_independent: false, image_count_billing: false }
describe('LLM model panel', () => {
 it('saves a disabled model and defaults its independent multiplier to one', async () => {
  setActivePinia(createPinia());vi.mocked(donebot.getModels).mockResolvedValue(structuredClone(fixture));vi.mocked(donebot.saveConfiguration).mockResolvedValue(structuredClone(fixture))
  const wrapper=mount(LlmModelsPanel);await flushPromises()
  expect(wrapper.get<HTMLInputElement>('[data-testid=price-gpt-test-rate_multiplier]').element.value).toBe('1')
  await wrapper.get('input[type=checkbox]').setValue(false)
  await wrapper.get('[data-test=save-configuration]').trigger('click');await flushPromises()
  expect(donebot.saveConfiguration).toHaveBeenCalledWith('llm', { models: [{ model: 'gpt-test', enabled: false, pricing: { rate_multiplier: 1 } }] })
 })
})
