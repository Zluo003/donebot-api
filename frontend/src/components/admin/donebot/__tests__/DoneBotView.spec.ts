import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DoneBotView from '@/views/admin/DoneBotView.vue'
import donebot from '@/api/admin/donebot'
const route = vi.hoisted(() => ({ query: { tab: 'storage' } }))
vi.mock('vue-router', () => ({ useRoute: () => route }))
vi.mock('@/api/admin/donebot', () => ({ default: { overview: vi.fn() } }))
const stubs = { AppLayout: { template: '<div><slot/></div>' }, LlmModelsPanel: true, ImageModelsPanel: true, AudioModelsPanel: true, VideoModelsPanel: true, StoragePanel: true, WebToolsPanel: true, CloudPanel: true, InstallList: true }
describe('DoneBot admin page', () => {
 it('shows all model and storage tabs without a stale static storage warning', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 }, asset_storage: { driver: 'local', public_base_url_configured: false } })
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  const tabs = wrapper.findAll('[role=tab]')
  expect(tabs.map(t => t.text())).toEqual(['LLM', '图片', '音频', '视频', '存储', '联网工具', 'Cloud 管理'])
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
  await tabs[5].trigger('click')
  expect(wrapper.findComponent({ name: 'WebToolsPanel' }).exists()).toBe(true)
  await tabs[1].trigger('click')
  expect(wrapper.find('[role=alert]').exists()).toBe(false)
  await tabs[4].trigger('click')
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
 })
 it.each([undefined, { audio: false, video: false }, { audio: true, video: true }])('shows media configuration without a separate runtime state (%j)', async features => {
  setActivePinia(createPinia())
  const overview = { ...(features ? { features } : {}), group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 } }
  vi.mocked(donebot.overview).mockResolvedValue(overview)
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  const tabs = wrapper.findAll('[role=tab]')
  expect(tabs.map(t => t.text())).toEqual(['LLM', '图片', '音频', '视频', '存储', '联网工具', 'Cloud 管理'])
  await tabs[2].trigger('click')
  expect(wrapper.findComponent({ name: 'AudioModelsPanel' }).exists()).toBe(true)
  expect(wrapper.text()).not.toContain('调用未启用')
  await tabs[3].trigger('click')
  expect(wrapper.findComponent({ name: 'VideoModelsPanel' }).exists()).toBe(true)
  expect(wrapper.text()).not.toContain('调用未启用')
 })
 it('opens the web tools settings from a direct admin tab link', async () => {
  setActivePinia(createPinia())
  route.query.tab = 'web-tools'
  const wrapper = mount(DoneBotView, { global: { stubs } }); await flushPromises()
  expect(wrapper.findComponent({ name: 'WebToolsPanel' }).exists()).toBe(true)
  expect(wrapper.findAll('[role=tab]')[5].attributes('aria-selected')).toBe('true')
  route.query.tab = 'storage'
 })
})
