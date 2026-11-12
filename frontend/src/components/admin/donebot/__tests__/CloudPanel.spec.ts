import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import CloudPanel from '../CloudPanel.vue'
import donebot from '@/api/admin/donebot'

vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { cloudPlan: vi.fn(), saveCloudPlan: vi.fn(), cloudEvents: vi.fn(), cloudNodes: vi.fn(), cloudInstances: vi.fn(), saveCloudNode: vi.fn(), changeCloudNodeCredential: vi.fn() } }))
beforeEach(() => {
  setActivePinia(createPinia()); vi.clearAllMocks()
  vi.mocked(donebot.cloudPlan).mockResolvedValue({ enabled: false, monthly_price: '0', retention_days: 7 })
  vi.mocked(donebot.cloudEvents).mockResolvedValue([])
  vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [], public_key: '' })
  vi.mocked(donebot.cloudInstances).mockResolvedValue([])
})
describe('Cloud control settings', () => {
  it('saves monthly price as an exact decimal string together with retention days', async () => {
    vi.mocked(donebot.saveCloudPlan).mockImplementation(async input => input)
    const wrapper = mount(CloudPanel); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[aria-label="Cloud 到期保留天数"]').element.value).toBe('7')
    await wrapper.get('[aria-label="Cloud 月费"]').setValue('19.123456')
    await wrapper.get('[aria-label="Cloud 到期保留天数"]').setValue(30)
    await wrapper.findAll('input[type=checkbox]')[0].setValue(true)
    await wrapper.findAll('form')[0].trigger('submit'); await flushPromises()
    expect(donebot.saveCloudPlan).toHaveBeenCalledWith({ enabled: true, monthly_price: '19.123456', retention_days: 30 })
    expect(wrapper.text()).toContain('自动永久删除')
  })
  it('does not allow saving unknown values when loading fails', async () => {
    vi.mocked(donebot.cloudPlan).mockRejectedValue(new Error('failed'))
    const wrapper = mount(CloudPanel); await flushPromises()
    await wrapper.findAll('form')[0].trigger('submit'); await flushPromises()
    expect(donebot.saveCloudPlan).not.toHaveBeenCalled()
    expect(wrapper.get('[aria-label="Cloud 月费"]').attributes('disabled')).toBeDefined()
  })
  it.each([false, true])('requires confirmation before changing a node credential (revoke=%s)', async revoke => {
    vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [{ id: 'fixture-node', name: 'Fixture node', endpoint: 'https://worker.example.test', capacity: 2, assigned: 1, enabled: true }], public_key: '' })
    vi.mocked(donebot.changeCloudNodeCredential).mockResolvedValue({ token: revoke ? '' : 'synthetic-new-node-token', revoked: revoke })
    const wrapper = mount(CloudPanel); await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === (revoke ? '撤销节点' : '轮换令牌'))!
    await button.trigger('click'); await flushPromises()
    expect(donebot.changeCloudNodeCredential).not.toHaveBeenCalled()
    const dialog = wrapper.get('[aria-label="节点凭据变更确认"]')
    expect(dialog.text()).toContain('旧令牌将立即失效')
    await dialog.findAll('button')[1].trigger('click'); await flushPromises()
    expect(donebot.changeCloudNodeCredential).toHaveBeenCalledWith('fixture-node', revoke)
    expect(wrapper.find('[aria-label="节点凭据变更确认"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="节点令牌"]').exists()).toBe(!revoke)
    if (!revoke) expect(wrapper.get<HTMLInputElement>('[aria-label="节点令牌"]').element.value).toBe('synthetic-new-node-token')
  })
})
