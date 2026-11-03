import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DoneBotView from '@/views/admin/DoneBotView.vue'
import donebot from '@/api/admin/donebot'
vi.mock('vue-router', () => ({ useRoute: () => ({ query: { tab: 'storage' } }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { overview: vi.fn() } }))
describe('DoneBot admin page', () => {
 it('shows model and storage tabs without using a stale static storage warning', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 }, asset_storage: { driver: 'local', public_base_url_configured: false } })
  const wrapper=mount(DoneBotView,{global:{stubs:{AppLayout:{template:'<div><slot/></div>'},LlmModelsPanel:true,ImageModelsPanel:true,StoragePanel:true,InstallList:true}}})
  await flushPromises()
  const tabs=wrapper.findAll('[role=tab]');expect(tabs.map(t=>t.text())).toEqual(['LLM','图片','存储'])
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
  await tabs[1].trigger('click')
  expect(wrapper.find('[role=alert]').exists()).toBe(false)
  await tabs[2].trigger('click')
  expect(wrapper.findComponent({ name: 'StoragePanel' }).exists()).toBe(true)
 })
 it('shows audio model management only when the audio feature is enabled', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ features: { audio: true, video: false }, group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 } })
  const wrapper=mount(DoneBotView,{global:{stubs:{AppLayout:{template:'<div><slot/></div>'},LlmModelsPanel:true,ImageModelsPanel:true,AudioModelsPanel:true,StoragePanel:true,InstallList:true}}})
  await flushPromises()
  const tabs=wrapper.findAll('[role=tab]');expect(tabs.map(t=>t.text())).toEqual(['LLM','图片','音频','存储'])
  await tabs[2].trigger('click')
  expect(wrapper.findComponent({ name: 'AudioModelsPanel' }).exists()).toBe(true)
 })
 it('shows video price management only when video is enabled', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ features: { audio: false, video: true }, group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 } })
  const wrapper=mount(DoneBotView,{global:{stubs:{AppLayout:{template:'<div><slot/></div>'},LlmModelsPanel:true,ImageModelsPanel:true,AudioModelsPanel:true,VideoModelsPanel:true,StoragePanel:true,InstallList:true}}})
  await flushPromises()
  const tabs=wrapper.findAll('[role=tab]');expect(tabs.map(t=>t.text())).toEqual(['LLM','图片','视频','存储'])
  await tabs[2].trigger('click')
  expect(wrapper.findComponent({ name: 'VideoModelsPanel' }).exists()).toBe(true)
 })
})
