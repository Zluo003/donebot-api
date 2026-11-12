import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CloudView from '../CloudView.vue'
import { cloudAPI, type CloudInstance } from '@/api/cloud'

vi.mock('@/api/cloud', () => ({ cloudAPI: { plan: vi.fn(), instances: vi.fn(), operations: vi.fn(), mutate: vi.fn(), launch: vi.fn(), revoke: vi.fn() } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { balance: 20 }, refreshUser: vi.fn(async () => ({})) }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['title'], template: '<section role="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' } }))
let wrapper: VueWrapper | undefined
const click = (text: string) => wrapper!.findAll('button').find(button => button.text() === text)!.trigger('click')
const instance: CloudInstance = { id: 'f7bf1b5b-24c8-4d0b-a2cd-fc76b64ad319', desired: 'running', actual: 'running', error: '', paid_until: new Date(Date.now() + 86400000).toISOString(), subscription_state: 'active', purge_locked: false }
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(cloudAPI.plan).mockResolvedValue({ plan: { enabled: true, monthly_price: '19.123456', retention_days: 7 }, browser_available: true })
  vi.mocked(cloudAPI.instances).mockResolvedValue([])
  vi.mocked(cloudAPI.operations).mockResolvedValue([])
  vi.mocked(cloudAPI.mutate).mockResolvedValue({ instance, operation: { id: 'op', action: 'start', state: 'pending', error: '' } })
})
afterEach(() => { wrapper?.unmount() })
describe('user Cloud console', () => {
  it('shows a refused payment and offers a newly confirmed retry without claiming success', async () => {
    wrapper = mount(CloudView); await flushPromises()
    await click('开通 Cloud')
    const failed = { id: 'op-failed', action: 'start', state: 'failed', error: 'billing_insufficient_balance' }
    vi.mocked(cloudAPI.mutate).mockResolvedValueOnce({ instance, operation: failed })
    vi.mocked(cloudAPI.instances).mockResolvedValue([{ ...instance, actual: 'error', error: failed.error, paid_until: null, subscription_state: '' }])
    vi.mocked(cloudAPI.operations).mockResolvedValue([failed])
    await click('确认'); await flushPromises()
    expect(wrapper.text()).toContain('未开通且未扣款')
    expect(wrapper.text()).not.toContain('操作已受理')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(wrapper.findAll('button').find(button => button.text() === '打开云实例')!.attributes('disabled')).toBeDefined()
    const oldKey = vi.mocked(cloudAPI.mutate).mock.calls[0][2]
    await click('重试开通')
    expect(cloudAPI.mutate).toHaveBeenCalledTimes(1)
    await click('确认'); await flushPromises()
    expect(vi.mocked(cloudAPI.mutate).mock.calls[1][2]).not.toBe(oldKey)
  })
  it('recognizes a cancelled subscription and confirms a fresh monthly charge before reopening', async () => {
    vi.mocked(cloudAPI.instances).mockResolvedValue([{ ...instance, desired: 'stopped', actual: 'stopped', subscription_state: 'cancelled' }])
    wrapper = mount(CloudView); await flushPromises()
    expect(wrapper.text()).toContain('已取消续费')
    expect(wrapper.findAll('button').some(button => button.text() === '取消自动续费')).toBe(false)
    await click('重新开通')
    expect(wrapper.get('[role="dialog"]').text()).toContain('开始新的一个月')
    expect(cloudAPI.mutate).not.toHaveBeenCalled()
    await click('确认'); await flushPromises()
    expect(cloudAPI.mutate).toHaveBeenCalledWith(instance.id, 'start', expect.any(String))
  })
  it('shows exact prepaid price and deletion terms, and charges only after confirmation', async () => {
    wrapper = mount(CloudView); await flushPromises()
    expect(wrapper.text()).toContain('19.123456')
    expect(wrapper.text()).toContain('永久删除实例与数据')
    await click('开通 Cloud')
    expect(cloudAPI.mutate).not.toHaveBeenCalled()
    await click('确认'); await flushPromises()
    expect(cloudAPI.mutate).toHaveBeenCalledWith(undefined, 'start', expect.any(String))
    expect(wrapper.text()).toContain('操作已受理')
  })
  it('keeps the same purchase key on a retry after a lost response', async () => {
    vi.mocked(cloudAPI.mutate).mockRejectedValueOnce(new Error('连接中断，请重试'))
    wrapper = mount(CloudView); await flushPromises(); await click('开通 Cloud'); await click('确认'); await flushPromises()
    expect(wrapper.get('[role="dialog"]').text()).toContain('连接中断')
    const key = vi.mocked(cloudAPI.mutate).mock.calls[0][2]
    await click('确认'); await flushPromises()
    expect(cloudAPI.mutate).toHaveBeenLastCalledWith(undefined, 'start', key)
  })
  it('requires confirmation before cancellation and describes immediate stop and data retention', async () => {
    vi.mocked(cloudAPI.instances).mockResolvedValue([instance])
    wrapper = mount(CloudView); await flushPromises(); await click('取消自动续费')
    expect(wrapper.get('[role="dialog"]').text()).toContain('立即停机')
    expect(cloudAPI.mutate).not.toHaveBeenCalled()
    await click('确认'); await flushPromises()
    expect(cloudAPI.mutate).toHaveBeenCalledWith(instance.id, 'cancel', expect.any(String))
  })
  it('cannot connect to an expired instance or create from unknown settings', async () => {
    vi.mocked(cloudAPI.instances).mockResolvedValue([{ ...instance, paid_until: new Date(Date.now() - 1000).toISOString() }])
    wrapper = mount(CloudView); await flushPromises()
    expect(wrapper.findAll('button').find(button => button.text() === '打开云实例')!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
    vi.mocked(cloudAPI.plan).mockRejectedValue(new Error('套餐暂不可用'))
    wrapper = mount(CloudView); await flushPromises()
    expect(wrapper.text()).toContain('套餐暂不可用'); expect(wrapper.text()).not.toContain('开通 Cloud')
  })
  it('reports launch errors and permits retry without exposing a gateway token', async () => {
    vi.mocked(cloudAPI.instances).mockResolvedValue([instance])
    vi.mocked(cloudAPI.launch).mockRejectedValue(new Error('实例当前不可用'))
    wrapper = mount(CloudView); await flushPromises(); await click('打开云实例'); await flushPromises()
    expect(cloudAPI.launch).toHaveBeenCalledWith(instance.id)
    expect(wrapper.text()).toContain('实例当前不可用')
    expect(wrapper.findAll('button').find(button => button.text() === '打开云实例')!.attributes('disabled')).toBeUndefined()
  })
})
