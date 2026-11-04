import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import WebToolsPanel from '../WebToolsPanel.vue'
import donebot, { type WebToolsSettingsInput } from '@/api/admin/donebot'

vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { webTools: vi.fn(), saveWebTools: vi.fn() } }))

beforeEach(() => {
 setActivePinia(createPinia())
 vi.clearAllMocks()
 vi.mocked(donebot.webTools).mockResolvedValue({ enabled: true, key_configured: true })
 vi.mocked(donebot.saveWebTools).mockResolvedValue({ enabled: true, key_configured: true })
})

describe('DoneBot web tool settings', () => {
 it('shows default enablement, key presence and DeepSeek priority with native fallback', async () => {
  const wrapper = mount(WebToolsPanel); await flushPromises()
  expect(wrapper.get<HTMLInputElement>('[aria-label="启用 DeepSeek Search / Fetch"]').element.checked).toBe(true)
  expect(wrapper.get('[role=status]').text()).toBe('API Key 已配置')
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  expect(wrapper.get('input[type=password]').attributes('placeholder')).toContain('留空保留')
  expect(wrapper.text()).toContain('优先使用 DeepSeek Search')
  expect(wrapper.text()).toContain('回退到模型原生搜索')
  expect(wrapper.text()).toContain('读取网页 URL 使用 Fetch')
 })

 it('preserves a configured key when the secret input is empty', async () => {
  let sent: WebToolsSettingsInput | undefined
  vi.mocked(donebot.saveWebTools).mockImplementation(async input => { sent = { ...input }; return { enabled: false, key_configured: true } })
  const wrapper = mount(WebToolsPanel); await flushPromises()
  await wrapper.get('[aria-label="启用 DeepSeek Search / Fetch"]').setValue(false)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(sent).toEqual({ enabled: false })
  expect(wrapper.get('[role=status]').text()).toBe('API Key 已配置')
 })

 it('sends a replacement key once and clears it immediately without browser persistence', async () => {
  const store = vi.spyOn(Storage.prototype, 'setItem')
  let sent: WebToolsSettingsInput | undefined
  let finish!: () => void
  vi.mocked(donebot.saveWebTools).mockImplementation(input => {
   sent = { ...input }
   return new Promise(resolve => { finish = () => resolve({ enabled: true, key_configured: true }) })
  })
  const wrapper = mount(WebToolsPanel); await flushPromises()
  const key = wrapper.get<HTMLInputElement>('input[type=password]')
  await key.setValue('new-private-deepseek-key')
  await wrapper.get('form').trigger('submit')
  expect(sent).toEqual({ enabled: true, api_key: 'new-private-deepseek-key' })
  expect(key.element.value).toBe('')
  expect(wrapper.html()).not.toContain('new-private-deepseek-key')
  expect(store).not.toHaveBeenCalled()
  await wrapper.get('form').trigger('submit')
  expect(donebot.saveWebTools).toHaveBeenCalledTimes(1)
  finish(); await flushPromises()
  expect(key.element.value).toBe('')
  expect(wrapper.get('[role=status]').text()).toBe('API Key 已配置')
  store.mockRestore()
 })

 it('requires the explicit clear action and never sends a replacement alongside it', async () => {
  let sent: WebToolsSettingsInput | undefined
  vi.mocked(donebot.saveWebTools).mockImplementation(async input => { sent = { ...input }; return { enabled: true, key_configured: false } })
  const wrapper = mount(WebToolsPanel); await flushPromises()
  await wrapper.get('input[type=password]').setValue('unsaved-key')
  await wrapper.get('[aria-label="清除已保存的 API Key"]').setValue(true)
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.disabled).toBe(true)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(sent).toEqual({ enabled: true, clear_api_key: true })
  expect(wrapper.get('[role=status]').text()).toBe('API Key 未配置')
  expect(wrapper.get<HTMLInputElement>('[aria-label="清除已保存的 API Key"]').element.checked).toBe(false)
 })

 it('does not reflect server secret errors and can reload after a failed read', async () => {
  vi.mocked(donebot.webTools).mockRejectedValueOnce(new Error('unsafe-key-from-server'))
  const wrapper = mount(WebToolsPanel); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('加载联网工具设置失败')
  expect(wrapper.html()).not.toContain('unsafe-key-from-server')
  expect(wrapper.get<HTMLButtonElement>('[data-testid=save-web-tools]').element.disabled).toBe(true)
  await wrapper.get('[data-testid=reload-web-tools]').trigger('click'); await flushPromises()
  expect(wrapper.get('[role=status]').text()).toBe('API Key 已配置')
  vi.mocked(donebot.saveWebTools).mockRejectedValueOnce(new Error('new-private-deepseek-key'))
  await wrapper.get('input[type=password]').setValue('new-private-deepseek-key')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toContain('保存联网工具设置失败')
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  expect(wrapper.html()).not.toContain('new-private-deepseek-key')
 })

 it('uses the existing step-up flow to retry the same key update', async () => {
  const sent: WebToolsSettingsInput[] = []
  vi.mocked(donebot.saveWebTools).mockImplementation(async input => {
   sent.push({ ...input })
   if (sent.length === 1) throw { code: 'STEP_UP_REQUIRED' }
   return { enabled: true, key_configured: true }
  })
  const wrapper = mount(WebToolsPanel); await flushPromises()
  await wrapper.get('input[type=password]').setValue('step-up-private-key')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
  expect(controller.visible.value).toBe(true)
  expect(wrapper.get<HTMLInputElement>('input[type=password]').element.value).toBe('')
  controller.onVerified(); await flushPromises()
  expect(sent).toEqual([{ enabled: true, api_key: 'step-up-private-key' }, { enabled: true, api_key: 'step-up-private-key' }])
  expect(wrapper.html()).not.toContain('step-up-private-key')
 })
})
