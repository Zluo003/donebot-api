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
import { APIMART_MODELS, APIMART_SERVICES, apimartAccountKind, isApimartAccount, type ApimartServiceKind } from '../apimartAccount'

const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const GroupSelector = defineComponent({ props: ['modelValue', 'groups', 'platform'], emits: ['update:modelValue'], template: '<div/>' })
function setup(account: Account | null = null, kind: ApimartServiceKind = account ? apimartAccountKind(account) : 'image') {
  return mount(ApimartAccountModal, { props: { show: true, account, kind, groups: [], proxies: [] }, global: { stubs: { BaseDialog: Dialog, GroupSelector, TotpStepUpDialog: true } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.run.mockImplementation((fn: () => unknown) => fn())
  mocks.create.mockResolvedValue({ id: 1 })
  mocks.update.mockResolvedValue({ id: 2 })
})

describe('APIMart account panel', () => {
  it.each(APIMART_SERVICES)('creates only $kind models and its own capability', async ({ kind, capability }) => {
    const wrapper = setup(null, kind)
    const models = APIMART_MODELS.filter(model => model.kind === kind)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-base-url"]').element.value).toBe('https://api.apimart.ai')
    expect(wrapper.findAll('[data-testid^="apimart-model-"]')).toHaveLength(models.length)
    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    for (const model of models) expect(wrapper.get<HTMLInputElement>(`[data-testid="apimart-model-${model.id}"]`).element.checked).toBe(true)
    await wrapper.get('[data-testid="apimart-name"]').setValue('APIMart upstream')
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('upstream-secret')
    wrapper.getComponent(GroupSelector).vm.$emit('update:modelValue', [7])
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.run).toHaveBeenCalledOnce()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'APIMart upstream', platform: 'openai', type: 'apikey', group_ids: [7],
      credentials: expect.objectContaining({ api_key: 'upstream-secret', base_url: 'https://api.apimart.ai', openai_capabilities: [capability], model_mapping: Object.fromEntries(models.map(model => [model.id, model.id])) })
    }))
    expect(wrapper.emitted('created')).toHaveLength(1)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
  })

  it('can enable only Seedream 5.5 without enabling legacy image models', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid="apimart-name"]').setValue('Seedream 5.5')
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('synthetic-key')
    for (const model of APIMART_MODELS.filter(model => model.kind === 'image')) {
      await wrapper.get(`[data-testid="apimart-model-${model.id}"]`).setValue(model.id === 'seedream-5-5')
    }
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ credentials: expect.objectContaining({
      model_mapping: { 'seedream-5-5': 'seedream-5-5' }, openai_capabilities: ['apimart_images']
    }) }))
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
    await wrapper.get('[data-testid="apimart-tab-music"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-suno"]').element.checked).toBe(false)
    await wrapper.get('[data-testid="apimart-tab-tts"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-gpt-4o-mini-tts"]').element.checked).toBe(false)
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('replacement')
    await wrapper.setProps({ show: false }); await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(3, expect.objectContaining({ credentials: { base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images'] } }))
  })

  it.each([
    { label: 'missing', mapping: {} },
    { label: 'empty', mapping: { model_mapping: {} } },
    { label: 'non-empty', mapping: { model_mapping: { 'my-image': 'seedream-5-0-flash', 'custom-model': 'custom-upstream' } } }
  ])('preserves an untouched $label model mapping when only the name changes', async ({ mapping }) => {
    const credentials = { base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images', 'apimart_tts'], ...mapping }
    const wrapper = setup({ id: 7, name: 'Original account', platform: 'openai', type: 'apikey', credentials } as Account)
    await wrapper.get('[data-testid="apimart-name"]').setValue('Renamed account')
    await wrapper.get('[data-testid="apimart-tab-tts"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(7, expect.objectContaining({ name: 'Renamed account', credentials }))
    expect(Object.hasOwn(mocks.update.mock.calls[0]![1].credentials, 'model_mapping')).toBe(Object.hasOwn(credentials, 'model_mapping'))
  })

  it('keeps aliases, other services and custom mappings while editing one service in a legacy account', async () => {
    const wrapper = setup({ id: 4, name: 'Existing multiple services', platform: 'openai', type: 'apikey', concurrency: 5,
      credentials: { api_key: 'must-not-display', openai_capabilities: ['apimart_images', 'apimart_suno', 'apimart_tts'],
        model_mapping: { 'my-image': 'seedream-5-0-flash', 'my-song': 'suno', 'my-voice': 'gpt-4o-mini-tts', 'custom-existing': 'custom-native-model' },
        future: { keep: true } } } as Account)
    expect(wrapper.find('[data-testid="apimart-existing-services"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="apimart-preserved-models"]').exists()).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-my-image"]').element.checked).toBe(true)
    await wrapper.get('[data-testid="apimart-model-seedream-5-0-flash"]').setValue(true)
    await wrapper.get('[data-testid="apimart-model-my-image"]').setValue(false)
    await wrapper.get('[data-testid="apimart-tab-music"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-my-song"]').element.checked).toBe(true)
    expect(wrapper.find('[data-testid="apimart-model-my-image"]').exists()).toBe(false)
    await wrapper.get('[data-testid="apimart-tab-tts"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-my-voice"]').element.checked).toBe(true)
    await wrapper.get('[data-testid="apimart-tab-image"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-my-image"]').element.checked).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-seedream-5-0-flash"]').element.checked).toBe(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(4, expect.objectContaining({ credentials: {
      base_url: 'https://api.apimart.ai', openai_capabilities: ['apimart_images', 'apimart_suno', 'apimart_tts'],
      model_mapping: { 'seedream-5-0-flash': 'seedream-5-0-flash', 'my-song': 'suno', 'my-voice': 'gpt-4o-mini-tts', 'custom-existing': 'custom-native-model' },
      future: { keep: true }
    } }))
    expect(wrapper.html()).not.toContain('must-not-display')
  })

  it('retains untouched service capabilities even when their models are absent from a legacy mapping', async () => {
    const wrapper = setup({ id: 5, name: 'Legacy mappings', platform: 'openai', type: 'apikey',
      credentials: { openai_capabilities: ['apimart_images', 'apimart_suno', 'apimart_tts'], model_mapping: { 'my-image': 'seedream-5.0-pro' } } } as Account)
    await wrapper.get('[data-testid="apimart-model-seedream-5-0-flash"]').setValue(true)
    await wrapper.get('[data-testid="apimart-tab-music"]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(5, expect.objectContaining({ credentials: expect.objectContaining({
      openai_capabilities: ['apimart_images', 'apimart_suno', 'apimart_tts'],
      model_mapping: { 'my-image': 'seedream-5.0-pro', 'seedream-5-0-flash': 'seedream-5-0-flash' }
    }) }))
  })

  it('adds a service only after its model is explicitly selected in the editor', async () => {
    const wrapper = setup({ id: 6, name: 'Images only', platform: 'openai', type: 'apikey',
      credentials: { openai_capabilities: ['apimart_images'], model_mapping: { 'my-image': 'seedream-5-0-flash' } } } as Account)
    await wrapper.get('[data-testid="apimart-tab-music"]').trigger('click')
    await wrapper.get('[data-testid="apimart-model-suno"]').setValue(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(6, expect.objectContaining({ credentials: expect.objectContaining({
      openai_capabilities: ['apimart_images', 'apimart_suno'], model_mapping: { 'my-image': 'seedream-5-0-flash', suno: 'suno' }
    }) }))
  })

  it('clears credentials and model selections when switching the creation entry', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('first-key')
    await wrapper.setProps({ kind: 'music' })
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-api-key"]').element.value).toBe('')
    expect(wrapper.find('[data-testid="apimart-model-seedream-5-0-flash"]').exists()).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-testid="apimart-model-suno"]').element.checked).toBe(true)
    await wrapper.get('[data-testid="apimart-name"]').setValue('Music only')
    await wrapper.get('[data-testid="apimart-api-key"]').setValue('same-upstream-key')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ credentials: expect.objectContaining({
      openai_capabilities: ['apimart_suno'], model_mapping: { suno: 'suno' }
    }) }))
  })

  it('uses the dedicated editor only for APIMart capability accounts', () => {
    const account = { platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['apimart_images'] } } as Account
    expect(isApimartAccount(account)).toBe(true)
    expect(isApimartAccount({ ...account, credentials: { openai_capabilities: ['images', 'chat_completions'] } })).toBe(false)
    expect(isApimartAccount({ ...account, platform: 'grok' })).toBe(false)
    expect(isApimartAccount({ ...account, credentials: { openai_capabilities: ['apimart_images', 'chat_completions'] } })).toBe(false)
  })
})
