import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import DoneBotView from '@/views/admin/DoneBotView.vue'
import donebot from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { overview: vi.fn() } }))
describe('DoneBot admin page', () => {
 it('shows only LLM and image tabs and warns for local storage without a public URL', async () => {
  setActivePinia(createPinia())
  vi.mocked(donebot.overview).mockResolvedValue({ group: { id: 1, name: 'DoneBot', platform: 'composite' }, user_count: 1, model_count: 2, install_count: 1, today_usage: { total_requests: 0, total_actual_cost: 0 }, asset_storage: { driver: 'local', public_base_url_configured: false } })
  const wrapper=mount(DoneBotView,{global:{stubs:{AppLayout:{template:'<div><slot/></div>'},LlmModelsPanel:true,ImageModelsPanel:true,InstallList:true}}})
  await flushPromises()
  const tabs=wrapper.findAll('[role=tab]');expect(tabs.map(t=>t.text())).toEqual(['LLM','图片'])
  await tabs[1].trigger('click')
  expect(wrapper.get('[role=alert]').text()).toContain('当前地址无法被上游拉取')
 })
})
