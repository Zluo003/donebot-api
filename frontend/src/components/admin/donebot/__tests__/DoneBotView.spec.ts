import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DoneBotView from '@/views/admin/DoneBotView.vue'
import donebot from '@/api/admin/donebot'
vi.mock('vue-router', () => ({ useRoute: () => ({ query: { tab: 'storage' } }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { overview: vi.fn() } }))
const stubs = { AppLayout: { template: '<div><slot/></div>' }, LlmModelsPanel: true, ImageModelsPanel: true, AudioModelsPanel: true, VideoModelsPanel: true, StoragePanel: true, InstallList: true }
describe('DoneBot admin page', () => {
 it('shows all model and storage tabs without a stale static storage warning', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 }, asset_storage: { driver: 'local', public_base_url_configured: false } })
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  const tabs = wrapper.findAll('[role=tab]')
  expect(tabs.map(t => t.text())).toEqual(['LLM', '图片', '音频', '视频', '存储'])
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
  await tabs[1].trigger('click')
  expect(wrapper.find('[role=alert]').exists()).toBe(false)
  await tabs[4].trigger('click')
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
 })
 it('allows audio and video configuration while their runtime features are disabled', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ features: { audio: false, video: false }, group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 } })
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  const tabs = wrapper.findAll('[role=tab]')
  expect(tabs.map(t => t.text())).toEqual(['LLM', '图片', '音频', '视频', '存储'])
  await tabs[2].trigger('click')
  expect(wrapper.findComponent({ name: 'AudioModelsPanel' }).exists()).toBe(true)
  expect(wrapper.text()).toContain('音频调用未启用，可先配置模型与价格')
  await tabs[3].trigger('click')
  expect(wrapper.findComponent({ name: 'VideoModelsPanel' }).exists()).toBe(true)
  expect(wrapper.text()).toContain('视频调用未启用，可先配置模型与价格')
 })
 it('omits disabled-feature hints when runtime features are enabled', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ features: { audio: true, video: true }, group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 } })
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  await wrapper.findAll('[role=tab]')[2].trigger('click')
  expect(wrapper.text()).not.toContain('调用未启用')
  await wrapper.findAll('[role=tab]')[3].trigger('click')
  expect(wrapper.text()).not.toContain('调用未启用')
 })
})
