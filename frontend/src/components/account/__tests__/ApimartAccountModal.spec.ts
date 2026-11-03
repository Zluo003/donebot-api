import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn(), error: vi.fn(), success: vi.fn(), run: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { create: mocks.create, update: mocks.update } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
vi.mock('@/composables/useStepUp', () => ({ useStepUp: () => ({ run: mocks.run }), isStepUpCancelled: () => false }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
import ApimartAccountModal from '../ApimartAccountModal.vue'
import { APIMART_MODELS, isApimartAccount } from '../apimartAccount'

const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const GroupSelector = defineComponent({ props: ['modelValue', 'groups', 'platform'], emits: ['update:modelValue'], template: '<div/>' })
function setup(account: Account | null = null) {
  return mount(ApimartAccountModal, { props: { show: true, account, groups: [], proxies: [] }, global: { stubs: { BaseDialog: Dialog, GroupSelector, TotpStepUpDialog: true } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.run.mockImplementation((fn: () => unknown) => fn())
  mocks.create.mockResolvedValue({ id: 1 })
  mocks.update.mockResolvedValue({ id: 2 })
})

describe('APIMart account panel', () => {
  it('creates an opt-in APIMart account with documented models and group binding', async () => {
    const wrapper = setup()
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-base-url"]').element.value).toBe('https://api.apimart.ai')
    expect(wrapper.findAll('[data-testid^="apimart-model-"]')).toHaveLength(APIMART_MODELS.length)
    await wrapper.get('[data-testid="apimart-name"]').setValue('APIMart upstream')
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('upstream-secret')
    wrapper.getComponent(GroupSelector).vm.$emit('update:modelValue', [7])
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.run).toHaveBeenCalledOnce()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'APIMart upstream', platform: 'openai', type: 'apikey', group_ids: [7],
      credentials: expect.objectContaining({ api_key: 'upstream-secret', base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images', 'apimart_suno', 'apimart_tts'], model_mapping: Object.fromEntries(APIMART_MODELS.map(model => [model.id, model.id])) })
    }))
    expect(wrapper.emitted('created')).toHaveLength(1)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
  })

  it('keeps existing secrets private and preserves model aliases and custom credentials', async () => {
    const wrapper = setup({ id: 2, name: 'Existing', platform: 'openai', type: 'apikey', concurrency: 3, group_ids: [7], proxy_id: 5,
      credentials: { api_key: 'must-not-display', base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images'], model_mapping: { 'my-image': 'seedream-5-0-flash' }, future: { keep: true } }
    } as Account)
    expect(wrapper.html()).not.toContain('must-not-display')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(2, expect.objectContaining({
      concurrency: 3, group_ids: [7], proxy_id: 5,
      credentials: { base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images'], model_mapping: { 'my-image': 'seedream-5-0-flash' }, future: { keep: true } }
    }))
  })

  it('rejects empty model selection and unsafe base URLs before writing', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid="apimart-name"]').setValue('APIMart')
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('secret')
    await wrapper.get('[data-testid="apimart-base-url"]').setValue('https://user:secret@api.apimart.ai')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.apimartAccount.invalidURL')
    expect(mocks.create).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="apimart-base-url"]').setValue('https://api.apimart.ai')
    for (const input of wrapper.findAll('[data-testid^="apimart-model-"]')) await input.setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.apimartAccount.selectModel')
    expect(mocks.create).not.toHaveBeenCalled()
  })

  it('preserves a legacy capability subset without broadening it and clears replacement keys on reopen', async () => {
    const wrapper = setup({ id: 3, name: 'Images only', platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['apimart_images'] } } as Account)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-seedream-5-0-flash"]').element.checked).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-suno"]').element.checked).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-gpt-4o-mini-tts"]').element.checked).toBe(false)
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('replacement')
    await wrapper.setProps({ show: false }); await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(3, expect.objectContaining({ credentials: expect.objectContaining({ openai_capabilities: ['apimart_images'], model_mapping: { 'seedream-5-0-flash': 'seedream-5-0-flash' } }) }))
  })

  it('uses the dedicated editor only for APIMart capability accounts', () => {
    const account = { platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['apimart_images'] } } as Account
    expect(isApimartAccount(account)).toBe(true)
    expect(isApimartAccount({ ...account, credentials: { openai_capabilities: ['images', 'chat_completions'] } })).toBe(false)
    expect(isApimartAccount({ ...account, platform: 'grok' })).toBe(false)
    expect(isApimartAccount({ ...account, credentials: { openai_capabilities: ['apimart_images', 'chat_completions'] } })).toBe(false)
  })
})
