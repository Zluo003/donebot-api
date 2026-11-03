import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import StoragePanel from '../StoragePanel.vue'
import donebot, { type StorageSettings } from '@/api/admin/donebot'

vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { storage: vi.fn(), saveStorage: vi.fn() } }))
const GiB = 1024 ** 3
const MiB = 1024 ** 2
const fixture = (): StorageSettings => ({
 upload: { enabled: true, driver: 'local', local_path: 'data/assets', public_base_url: '', max_storage_bytes: 10 * GiB, ttl_hours: 168, cleanup_threshold_percent: 90, max_file_bytes: 32 * MiB, usage_bytes: GiB, file_count: 3, secret_configured: false, s3: { endpoint: '', region: 'auto', bucket: '', prefix: '', access_key_id: '', secret_access_key: '', force_path_style: false } },
 generated: { enabled: true, driver: 's3', local_path: 'data/generated', public_base_url: 'https://cdn.example.com', max_storage_bytes: 50 * GiB, ttl_hours: 168, cleanup_threshold_percent: 90, max_file_bytes: 256 * MiB, usage_bytes: 2 * GiB, file_count: 7, secret_configured: true, s3: { endpoint: 'https://s3.example.com', region: 'auto', bucket: 'generated', prefix: 'media/', access_key_id: 'id-example', secret_access_key: '', force_path_style: true } }
})
beforeEach(() => {
 setActivePinia(createPinia())
 vi.clearAllMocks()
 vi.mocked(donebot.storage).mockResolvedValue(fixture())
 vi.mocked(donebot.saveStorage).mockImplementation(async value => structuredClone(value))
})
describe('DoneBot storage management', () => {
 it('shows separate upload and generated policies with shared media scope and usage', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  expect(wrapper.get('[data-testid=storage-upload]').text()).toContain('上传存储')
  const generated = wrapper.get('[data-testid=storage-generated]')
  expect(generated.text()).toContain('生成产物存储')
  expect(generated.text()).toContain('视频、音乐和异步图片')
  expect(generated.text()).toContain('2 GiB / 50 GiB')
  expect(generated.text()).toContain('7 个文件')
  expect(wrapper.findAll('input[type=password]').map(input => (input.element as HTMLInputElement).value)).toEqual([''])
  expect(wrapper.get('[data-testid=generated-secret]').attributes('placeholder')).toContain('已配置')
 })
 it('saves each policy, converts displayed capacity units and retains an unchanged secret', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=upload-capacity]').setValue('12.5')
  await wrapper.get('[data-testid=upload-max-file]').setValue('64')
  await wrapper.get('[data-testid=generated-driver]').setValue('local')
  await wrapper.get('[data-testid=generated-local-path]').setValue('data/output')
  await wrapper.get('[data-testid=generated-public-url]').setValue('https://gateway.example.com')
  await wrapper.get('[data-testid=generated-ttl]').setValue('48')
  await wrapper.get('[data-testid=generated-threshold]').setValue('85')
  await wrapper.get('[data-testid=save-storage]').trigger('click'); await flushPromises()
  expect(donebot.saveStorage).toHaveBeenCalledTimes(1)
  const sent = vi.mocked(donebot.saveStorage).mock.calls[0][0]
  expect(sent.upload.max_storage_bytes).toBe(12.5 * GiB)
  expect(sent.upload.max_file_bytes).toBe(64 * MiB)
  expect(sent.generated).toMatchObject({ driver: 'local', local_path: 'data/output', public_base_url: 'https://gateway.example.com', ttl_hours: 48, cleanup_threshold_percent: 85, s3: { secret_access_key: '' } })
  expect(sent.generated).not.toHaveProperty('usage_bytes')
  expect(sent.generated).not.toHaveProperty('secret_configured')
 })
 it('allows replacing a secret but clears it from the form after a successful save', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=generated-secret]').setValue('new-private-secret')
  await wrapper.get('[data-testid=save-storage]').trigger('click'); await flushPromises()
  expect(vi.mocked(donebot.saveStorage).mock.calls[0][0].generated.s3.secret_access_key).toBe('new-private-secret')
  expect(wrapper.get<HTMLInputElement>('[data-testid=generated-secret]').element.value).toBe('')
  expect(wrapper.html()).not.toContain('new-private-secret')
 })
 it('blocks invalid capacity, TTL, threshold and missing generated local public URL', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  const save = wrapper.get<HTMLButtonElement>('[data-testid=save-storage]')
  for (const [field, invalid, valid] of [['upload-capacity', '0', '10'], ['generated-ttl', '8761', '168'], ['generated-threshold', '0', '90']]) {
   await wrapper.get(`[data-testid=${field}]`).setValue(invalid)
   expect(save.element.disabled).toBe(true)
   await wrapper.get(`[data-testid=${field}]`).setValue(valid)
  }
  await wrapper.get('[data-testid=generated-driver]').setValue('local')
  await wrapper.get('[data-testid=generated-public-url]').setValue('')
  expect(save.element.disabled).toBe(true)
  expect(wrapper.get('[role=alert]').text()).toContain('公网')
  expect(donebot.saveStorage).not.toHaveBeenCalled()
 })
 it('refreshes usage without discarding an unsaved policy or secret', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=upload-capacity]').setValue('15')
  await wrapper.get('[data-testid=generated-secret]').setValue('unsaved-secret')
  const refreshed = fixture(); refreshed.generated.usage_bytes = 3 * GiB
  vi.mocked(donebot.storage).mockResolvedValueOnce(refreshed)
  await wrapper.get('[data-testid=reload-storage]').trigger('click'); await flushPromises()
  expect(wrapper.get<HTMLInputElement>('[data-testid=upload-capacity]').element.value).toBe('15')
  expect(wrapper.get<HTMLInputElement>('[data-testid=generated-secret]').element.value).toBe('unsaved-secret')
  expect(wrapper.get('[data-testid=storage-generated]').text()).toContain('3 GiB / 50 GiB')
 })
 it('requires S3 credentials only while that policy is enabled', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=upload-driver]').setValue('s3')
  expect(wrapper.get<HTMLButtonElement>('[data-testid=save-storage]').element.disabled).toBe(true)
  await wrapper.get('[aria-label=启用上传存储]').setValue(false)
  expect(wrapper.get<HTMLButtonElement>('[data-testid=save-storage]').element.disabled).toBe(false)
 })
 it('keeps the draft and displays an update failure for retry', async () => {
  vi.mocked(donebot.saveStorage).mockRejectedValueOnce(new Error('连接存储失败'))
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=upload-capacity]').setValue('15')
  await wrapper.get('[data-testid=save-storage]').trigger('click'); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('连接存储失败')
  expect(wrapper.get<HTMLInputElement>('[data-testid=upload-capacity]').element.value).toBe('15')
 })
 it('requires a public URL for S3 retention longer than seven days', async () => {
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=generated-public-url]').setValue('')
  await wrapper.get('[data-testid=generated-ttl]').setValue('169')
  expect(wrapper.get<HTMLButtonElement>('[data-testid=save-storage]').element.disabled).toBe(true)
  expect(wrapper.get('[role=alert]').text()).toContain('168')
  await wrapper.get('[data-testid=generated-public-url]').setValue('https://cdn.example.com')
  expect(wrapper.get<HTMLButtonElement>('[data-testid=save-storage]').element.disabled).toBe(false)
 })
 it('prompts for existing step-up verification before retrying the same storage update', async () => {
  vi.mocked(donebot.saveStorage).mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
  const wrapper = mount(StoragePanel); await flushPromises()
  await wrapper.get('[data-testid=save-storage]').trigger('click'); await flushPromises()
  const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
  expect(controller.visible.value).toBe(true)
  controller.onVerified(); await flushPromises()
  expect(donebot.saveStorage).toHaveBeenCalledTimes(2)
  expect(vi.mocked(donebot.saveStorage).mock.calls[1][0]).toEqual(vi.mocked(donebot.saveStorage).mock.calls[0][0])
 })
 it('shows load failures and offers retry', async () => {
  vi.mocked(donebot.storage).mockRejectedValueOnce(new Error('加载存储失败'))
  const wrapper = mount(StoragePanel); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('加载存储失败')
  await wrapper.get('[data-testid=reload-storage]').trigger('click'); await flushPromises()
  expect(wrapper.find('[data-testid=storage-upload]').exists()).toBe(true)
 })
})
