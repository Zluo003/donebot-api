import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import MobileConnectionSettings from '../MobileConnectionSettings.vue'
import { mobileSettingsAPI, type MobileSettings, type MobileSettingsInput } from '@/api/admin/mobile-settings'
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/mobile-settings', () => ({ mobileSettingsAPI: { get: vi.fn(), save: vi.fn() } }))
const existing: MobileSettings = { relay_configured: true, relay_kid: 'k1', push_enabled: false, push_configured: false, apns_key_id: 'TESTKEY123', apns_team_id: 'TESTTEAM12', apns_key_configured: true, encryption_configured: true, source: 'database' }
let wrapper: ReturnType<typeof mount>
beforeEach(() => { vi.clearAllMocks(); vi.mocked(mobileSettingsAPI.get).mockResolvedValue({ ...existing }); vi.mocked(mobileSettingsAPI.save).mockResolvedValue({ ...existing }) })
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })
describe('mobile connection administration', () => {
 it('preserves stored private keys when enabling push and emits a node refresh', async () => {
  wrapper = mount(MobileConnectionSettings); await flushPromises()
  expect(wrapper.text()).toContain('已保存私钥')
  await wrapper.get('input[type=checkbox]').setValue(true)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(mobileSettingsAPI.save).toHaveBeenCalledWith(expect.objectContaining({ relay_kid: 'k1', generate_relay: false, relay_seed: undefined, push_enabled: true, apns_key: '' }))
  expect(wrapper.emitted('saved')).toHaveLength(1)
 })
 it('generates an initial relay key without sending a browser-generated secret', async () => {
  vi.mocked(mobileSettingsAPI.get).mockResolvedValue({ ...existing, relay_configured: false, relay_kid: '', apns_key_configured: false })
  wrapper = mount(MobileConnectionSettings); await flushPromises()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(mobileSettingsAPI.save).toHaveBeenCalledWith(expect.objectContaining({ relay_kid: 'k1', generate_relay: true, relay_seed: undefined }))
 })
 it('rejects signing replacement with the current Key ID before sending', async () => {
  wrapper = mount(MobileConnectionSettings); await flushPromises()
  await wrapper.get('select').setValue('generate')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('新的 Key ID')
  expect(mobileSettingsAPI.save).not.toHaveBeenCalled()
 })
 it('clears imported secrets and retries the same request after step-up', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const sent: MobileSettingsInput[] = []
  vi.mocked(mobileSettingsAPI.save).mockImplementation(async input => { sent.push({ ...input }); if (sent.length === 1) throw { reason: 'STEP_UP_REQUIRED' }; return { ...existing, relay_kid: 'k2' } })
  wrapper = mount(MobileConnectionSettings); await flushPromises()
  await wrapper.get('select').setValue('import')
  await wrapper.get('input[placeholder=k1]').setValue('k2')
  const seed = 'a'.repeat(43)
  await wrapper.get('input[type=password]').setValue(seed)
  const file = new File(['private'], 'AuthKey_TESTKEY123.p8')
  Object.defineProperty(file, 'text', { value: async () => '-----BEGIN PRIVATE KEY-----\nsynthetic-p8\n-----END PRIVATE KEY-----' })
  Object.defineProperty(wrapper.get('input[type=file]').element, 'files', { value: [file] })
  await wrapper.get('input[type=file]').trigger('change'); await flushPromises()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  expect(wrapper.html()).not.toContain('synthetic-p8')
  const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
  expect(controller.visible.value).toBe(true)
  controller.onVerified(); await flushPromises()
  expect(sent).toHaveLength(2); expect(sent[1]).toEqual(sent[0]); expect(sent[0].relay_seed).toBe(seed); expect(sent[0].apns_key).toContain('synthetic-p8')
 })
 it('blocks secret persistence with an ephemeral infrastructure key', async () => {
  vi.mocked(mobileSettingsAPI.get).mockResolvedValue({ ...existing, encryption_configured: false })
  wrapper = mount(MobileConnectionSettings); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('TOTP_ENCRYPTION_KEY')
  expect(wrapper.get<HTMLButtonElement>('button[type=submit]').element.disabled).toBe(true)
  await wrapper.get('form').trigger('submit'); expect(mobileSettingsAPI.save).not.toHaveBeenCalled()
 })
})
