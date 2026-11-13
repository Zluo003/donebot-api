import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import CloudPanel from '../CloudPanel.vue'
import donebot from '@/api/admin/donebot'

vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { cloudPlan: vi.fn(), saveCloudPlan: vi.fn(), cloudEvents: vi.fn(), cloudNodes: vi.fn(), cloudInstances: vi.fn(), saveCloudNode: vi.fn(), changeCloudNodeCredential: vi.fn(), deleteCloudNode: vi.fn() } }))
const publicKey = 'synthetic-master-public-key'
const fixtureNode = { id: 'fixture-node', name: 'Fixture node', endpoint: 'https://worker.example.test', capacity: 2, assigned: 0, enabled: true }
beforeEach(() => {
  setActivePinia(createPinia()); vi.resetAllMocks()
  vi.mocked(donebot.cloudPlan).mockResolvedValue({ enabled: false, monthly_price: '0', retention_days: 7 })
  vi.mocked(donebot.cloudEvents).mockResolvedValue([])
  vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [], public_key: publicKey })
  vi.mocked(donebot.cloudInstances).mockResolvedValue([])
  vi.mocked(donebot.deleteCloudNode).mockResolvedValue(undefined)
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
    vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [{ ...fixtureNode, assigned: 1 }], public_key: publicKey })
    vi.mocked(donebot.changeCloudNodeCredential).mockResolvedValue({ token: revoke ? '' : 'synthetic-new-node-token', public_key: publicKey })
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
    if (!revoke) {
      expect(wrapper.get<HTMLInputElement>('[aria-label="节点令牌"]').element.value).toBe('synthetic-new-node-token')
      expect(wrapper.get<HTMLInputElement>('[aria-label="节点配置主控公钥"]').element.value).toBe(publicKey)
    }
  })
  it('shows the public key together with the one-time token even if the following refresh fails', async () => {
    vi.mocked(donebot.saveCloudNode).mockResolvedValue({ node: { id: fixtureNode.id }, token: 'synthetic-node-token', public_key: publicKey })
    const wrapper = mount(CloudPanel); await flushPromises()
    vi.mocked(donebot.cloudNodes).mockRejectedValue(new Error('offline'))
    const form = wrapper.findAll('form')[1]
    const inputs = form.findAll('input')
    await inputs[0].setValue('Fixture node'); await inputs[1].setValue(fixtureNode.endpoint)
    await form.trigger('submit'); await flushPromises()
    expect(donebot.saveCloudNode).toHaveBeenCalledWith({ name: 'Fixture node', endpoint: fixtureNode.endpoint, capacity: 10, enabled: true })
    expect(wrapper.get<HTMLInputElement>('[aria-label="节点令牌"]').element.value).toBe('synthetic-node-token')
    expect(wrapper.get<HTMLInputElement>('[aria-label="节点配置主控公钥"]').element.value).toBe(publicKey)
    await wrapper.findAll('button').find(b => b.text() === '已保存，隐藏令牌')!.trigger('click')
    expect(wrapper.find('[aria-label="节点令牌"]').exists()).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[aria-label="主控公钥"]').element.value).toBe(publicKey)
  })
  it('explains a missing signing seed while keeping the nodes available for deletion', async () => {
    vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [fixtureNode], public_key: '' })
    const wrapper = mount(CloudPanel); await flushPromises()
    expect(wrapper.text()).toContain('DONEBOT_CLOUD_SIGNING_SEED')
    expect(wrapper.text()).toContain(fixtureNode.name)
    expect(wrapper.findAll('button').find(b => b.text() === '添加服务器')!.attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(b => b.text() === '轮换令牌')!.attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(b => b.text() === '删除服务器')!.attributes('disabled')).toBeUndefined()
    await wrapper.findAll('form')[1].trigger('submit'); await flushPromises()
    expect(donebot.saveCloudNode).not.toHaveBeenCalled()
  })
  it('allows cancellation and requires confirmation before removing an empty node', async () => {
    vi.mocked(donebot.cloudNodes).mockResolvedValueOnce({ nodes: [fixtureNode], public_key: publicKey }).mockResolvedValue({ nodes: [], public_key: publicKey })
    const wrapper = mount(CloudPanel); await flushPromises()
    const remove = wrapper.findAll('button').find(b => b.text() === '删除服务器')!
    await remove.trigger('click')
    expect(donebot.deleteCloudNode).not.toHaveBeenCalled()
    let dialog = wrapper.get('[aria-label="删除服务器确认"]')
    expect(dialog.text()).toContain('历史实例和账单记录保留')
    await dialog.findAll('button')[0].trigger('click')
    expect(wrapper.find('[aria-label="删除服务器确认"]').exists()).toBe(false)
    expect(donebot.deleteCloudNode).not.toHaveBeenCalled()
    await remove.trigger('click')
    dialog = wrapper.get('[aria-label="删除服务器确认"]')
    await dialog.findAll('button')[1].trigger('click'); await flushPromises()
    expect(donebot.deleteCloudNode).toHaveBeenCalledWith(fixtureNode.id)
    expect(wrapper.find('[aria-label="删除服务器确认"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(fixtureNode.name)
  })
  it('blocks deleting assigned nodes and explains retained or pending instances', async () => {
    vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [{ ...fixtureNode, assigned: 1 }], public_key: publicKey })
    const wrapper = mount(CloudPanel); await flushPromises()
    expect(wrapper.findAll('button').find(b => b.text() === '删除服务器')!.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('含停机和待删除实例')
    expect(donebot.deleteCloudNode).not.toHaveBeenCalled()
  })
  it('preserves a node when the server reports a concurrent assignment', async () => {
    vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [fixtureNode], public_key: publicKey })
    vi.mocked(donebot.deleteCloudNode).mockRejectedValue({ reason: 'CLOUD_NODE_IN_USE' })
    const wrapper = mount(CloudPanel); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '删除服务器')!.trigger('click')
    await wrapper.get('[aria-label="删除服务器确认"]').findAll('button')[1].trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('该服务器仍有实例')
    expect(wrapper.text()).toContain(fixtureNode.name)
    expect(wrapper.find('[aria-label="删除服务器确认"]').exists()).toBe(true)
  })
})
