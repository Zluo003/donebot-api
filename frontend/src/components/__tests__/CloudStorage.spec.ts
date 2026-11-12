import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CloudStorage from '../CloudStorage.vue'
import { cloudAPI } from '@/api/cloud'
vi.mock('@/api/cloud', () => ({ cloudAPI: { resources: vi.fn(), backups: vi.fn(), backup: vi.fn() } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['title'], template: '<section role="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' } }))
let wrapper: VueWrapper | undefined
const click = (text: string) => wrapper!.findAll('button').find(button => button.text() === text)!.trigger('click')
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(cloudAPI.resources).mockResolvedValue({ cpu_cores: 1, memory_bytes: 2147483648, pids: 256, storage: { enforced: true, limit_bytes: 5368709120, available_bytes: 1024 } })
  vi.mocked(cloudAPI.backups).mockResolvedValue([{ id: 'snapshot', state: 'ready', created_at: new Date().toISOString(), bytes: 5368709120 }])
  vi.mocked(cloudAPI.backup).mockResolvedValue()
})
afterEach(() => wrapper?.unmount())
describe('Cloud storage', () => {
  it('shows enforced storage, warns when full, and confirms before overwriting data', async () => {
    wrapper = mount(CloudStorage, { props: { id: 'instance' } }); await flushPromises()
    expect(wrapper.text()).toContain('5.00 GiB 磁盘上限'); expect(wrapper.text()).toContain('磁盘空间不足')
    await click('恢复此备份'); expect(wrapper.get('[role="dialog"]').text()).toContain('覆盖')
    expect(cloudAPI.backup).not.toHaveBeenCalled()
    await click('确认'); await flushPromises()
    expect(cloudAPI.backup).toHaveBeenCalledWith('instance', 'restore', expect.any(String), 'snapshot')
    expect(wrapper.emitted('busy')).toEqual([[true], [false]])
  })
  it('retries a lost backup response with the same key', async () => {
    vi.mocked(cloudAPI.backup).mockRejectedValueOnce(new Error('lost response'))
    wrapper = mount(CloudStorage, { props: { id: 'instance' } }); await flushPromises()
    await click('创建备份'); await click('确认'); await flushPromises()
    const key = vi.mocked(cloudAPI.backup).mock.calls[0][2]
    await click('确认'); await flushPromises()
    expect(cloudAPI.backup).toHaveBeenLastCalledWith('instance', 'create', key, undefined)
  })
})
