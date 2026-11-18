import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClientReleaseStoragePanel from '../ClientReleaseStoragePanel.vue'
import donebot, { type ClientReleaseStorageSettings } from '@/api/admin/donebot'
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', async importOriginal => ({ ...(await importOriginal<typeof import('@/api/admin/donebot')>()), default: { clientReleaseStorage: vi.fn(), saveClientReleaseStorage: vi.fn(), testClientReleaseStorage: vi.fn() } }))
const fixture = (): ClientReleaseStorageSettings => ({ driver: 'local', local_path: 'fixture/packages', secret_configured: true, s3: { endpoint: 'https://storage.example', region: 'auto', bucket: 'packages', prefix: 'downloads/', access_key_id: 'fixture-key', secret_access_key: '', force_path_style: false, public_base_url: '' } })
beforeEach(() => { vi.clearAllMocks(); vi.mocked(donebot.clientReleaseStorage).mockResolvedValue(fixture()); vi.mocked(donebot.saveClientReleaseStorage).mockResolvedValue(fixture()) })
describe('independent client release storage', () => {
 it('saves a dedicated directory without media retention settings', async () => {
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  await wrapper.get('[data-testid=release-storage-path]').setValue('fixture/new-packages')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(donebot.saveClientReleaseStorage).toHaveBeenCalledWith(expect.objectContaining({ local_path: 'fixture/new-packages', driver: 'local' }))
  const input = vi.mocked(donebot.saveClientReleaseStorage).mock.calls[0][0]
  expect(input).not.toHaveProperty('upload'); expect(input).not.toHaveProperty('ttl_hours')
  expect(wrapper.get('[role=status]').text()).toContain('已保存')
 })
 it('preserves an existing secret, retries step-up and clears replacement from the form', async () => {
  const calls: ClientReleaseStorageSettings[] = []
  vi.mocked(donebot.saveClientReleaseStorage).mockImplementation(async input => { calls.push(input); if (calls.length === 1) throw { code: 'STEP_UP_REQUIRED' }; return { ...fixture(), driver: 's3' } })
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  await wrapper.get('[data-testid=release-storage-driver]').setValue('s3')
  const key = wrapper.get<HTMLInputElement>('[data-testid=release-storage-secret]')
  expect(key.element.value).toBe(''); expect(key.attributes('placeholder')).toContain('留空保留')
  await key.setValue('synthetic-replacement')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(key.element.value).toBe('')
  const controller = wrapper.getComponent({name:'TotpStepUpDialog'}).props('controller')
  expect(controller.visible.value).toBe(true); controller.onVerified(); await flushPromises()
  expect(calls).toHaveLength(2); expect(calls[1].s3.secret_access_key).toBe('synthetic-replacement')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(calls[2].s3.secret_access_key).toBe('')
 })
 it('does not expose provider secrets through errors and supports read retry', async () => {
  vi.mocked(donebot.clientReleaseStorage).mockRejectedValueOnce(new Error('secret-in-provider-error'))
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  expect(wrapper.text()).not.toContain('secret-in-provider-error')
  await wrapper.get('button').trigger('click'); await flushPromises()
  expect(wrapper.get('[data-testid=release-storage-path]').exists()).toBe(true)
 })
})

describe('release storage connectivity', () => {
 it('tests a draft without saving or clearing credentials and invalidates stale results', async () => {
  vi.mocked(donebot.testClientReleaseStorage).mockResolvedValue({ ok: true, message: '测试文件已清理，配置未保存' })
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  await wrapper.get('[data-testid=release-storage-driver]').setValue('s3')
  await wrapper.get('[data-testid=release-storage-bucket]').setValue('draft-bucket')
  await wrapper.get('[data-testid=release-storage-secret]').setValue('synthetic-key')
  await wrapper.get('[data-testid=test-release-storage]').trigger('click'); await flushPromises()
  expect(donebot.saveClientReleaseStorage).not.toHaveBeenCalled()
  expect(donebot.testClientReleaseStorage).toHaveBeenCalledWith(expect.objectContaining({ s3: expect.objectContaining({ bucket: 'draft-bucket', secret_access_key: 'synthetic-key' }) }))
  expect(wrapper.get('[data-testid=release-storage-test-result]').text()).toContain('配置未保存')
  expect(wrapper.get<HTMLInputElement>('[data-testid=release-storage-secret]').element.value).toBe('synthetic-key')
  await wrapper.get('[data-testid=release-storage-bucket]').setValue('different')
  expect(wrapper.find('[data-testid=release-storage-test-result]').exists()).toBe(false)
 })
 it('retries via step-up and hides transport secrets on error', async () => {
  vi.mocked(donebot.testClientReleaseStorage).mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce({ ok: false, message: '下载链接无法访问' })
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  await wrapper.get('[data-testid=release-storage-driver]').setValue('s3')
  await wrapper.get('[data-testid=test-release-storage]').trigger('click'); await flushPromises()
  const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
  expect(controller.visible.value).toBe(true); controller.onVerified(); await flushPromises()
  expect(donebot.testClientReleaseStorage).toHaveBeenCalledTimes(2)
  expect(wrapper.get('[data-testid=release-storage-test-result]').attributes('role')).toBe('alert')
  vi.mocked(donebot.testClientReleaseStorage).mockRejectedValueOnce(new Error('secret-in-transport'))
  await wrapper.get('[data-testid=test-release-storage]').trigger('click'); await flushPromises()
  expect(wrapper.text()).not.toContain('secret-in-transport')
 })
 it('offers the independent OSS upload and regional download configuration', async () => {
  const wrapper = mount(ClientReleaseStoragePanel); await flushPromises()
  await wrapper.get('[data-testid=release-storage-driver]').setValue('oss')
  expect(wrapper.find('[data-testid=release-oss-upload-endpoint]').exists()).toBe(true)
  expect(wrapper.find('[data-testid=release-oss-download-endpoint]').exists()).toBe(true)
  expect(wrapper.text()).toContain('私有桶')
 })
})
