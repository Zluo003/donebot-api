import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn(), error: vi.fn(), success: vi.fn(), run: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { create: mocks.create, update: mocks.update } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success }) }))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: mocks.run }), isStepUpCancelled: () => false
}))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
import VideoAccountModal from '../VideoAccountModal.vue'
import { isVideoAccount, SEEDANCE_MODELS, createSeedanceModelMappings } from '../videoAccount'

const Dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const GroupSelector = defineComponent({ props: ['modelValue', 'groups', 'platform'], emits: ['update:modelValue'], template: '<div/>' })
function setup(account: Account | null = null) {
  return mount(VideoAccountModal, {
    props: { show: true, account, groups: [], proxies: [] },
    global: { stubs: { BaseDialog: Dialog, GroupSelector, TotpStepUpDialog: true } }
  })
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.run.mockImplementation((fn: () => unknown) => fn())
  mocks.create.mockResolvedValue({ id: 1 })
  mocks.update.mockResolvedValue({ id: 2 })
})

describe('Video account panel', () => {
  it('creates Muse from its service tab with a custom downstream alias and no Seedance settings', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-provider-muse]').trigger('click')
    expect(wrapper.get('[data-testid=video-provider-muse]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[role=tabpanel]').attributes('aria-labelledby')).toBe('video-provider-muse')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-base-url]').element.value).toBe('https://token.secure-skill.com')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-upstream-0]').element.value).toBe('muse-video')
    expect(wrapper.findAll('[data-testid^=video-model-enabled-]')).toHaveLength(1)
    expect(wrapper.findAll('[data-testid^=video-resolution-]')).toHaveLength(0)
    expect(wrapper.find('[data-testid=video-model-spec-0]').exists()).toBe(false)
    expect(wrapper.find('[data-testid=video-output-storage]').exists()).toBe(false)
    await wrapper.get('[data-testid=video-name]').setValue('Muse upstream')
    await wrapper.get('[data-testid=video-api-key]').setValue('synthetic-muse-key')
    await wrapper.get('[data-testid=video-model-downstream-0]').setValue('public-muse')
    wrapper.getComponent(GroupSelector).vm.$emit('update:modelValue', [7])
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Muse upstream', platform: 'openai', type: 'apikey', group_ids: [7], extra: {},
      credentials: {
        api_key: 'synthetic-muse-key', base_url: 'https://token.secure-skill.com',
        openai_capabilities: ['muse'], model_mapping: { 'public-muse': 'muse-video' }
      }
    }))
    expect(mocks.run).toHaveBeenCalledOnce()
    expect(wrapper.emitted('created')).toHaveLength(1)
  })

  it('keeps upstream drafts and keys separate across tabs and clears them when closed', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-base-url]').setValue('https://seedance.example.test/v3')
    await wrapper.get('[data-testid=video-api-key]').setValue('synthetic-seedance-key')
    await wrapper.get('[data-testid=video-model-downstream-0]').setValue('my-seedance')
    await wrapper.get('[data-testid=video-resolution-1080p]').setValue(true)
    await wrapper.get('[data-testid=video-provider-muse]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('')
    await wrapper.get('[data-testid=video-base-url]').setValue('https://muse.example.test/v1')
    await wrapper.get('[data-testid=video-api-key]').setValue('synthetic-muse-key')
    await wrapper.get('[data-testid=video-model-downstream-0]').setValue('my-muse')
    await wrapper.get('[data-testid=video-provider-seedance]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-base-url]').element.value).toBe('https://seedance.example.test/v3')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('synthetic-seedance-key')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-downstream-0]').element.value).toBe('my-seedance')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-resolution-1080p]').element.checked).toBe(true)
    await wrapper.get('[data-testid=video-provider-muse]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-base-url]').element.value).toBe('https://muse.example.test/v1')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('synthetic-muse-key')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-downstream-0]').element.value).toBe('my-muse')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('')
    await wrapper.get('[data-testid=video-provider-muse]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-base-url]').element.value).toBe('https://token.secure-skill.com')
  })

  it('supports keyboard navigation between service tabs', async () => {
    const wrapper = setup()
    await wrapper.get('[role=tablist]').trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('[data-testid=video-provider-muse]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid=video-provider-muse]').attributes('tabindex')).toBe('0')
    await wrapper.get('[role=tablist]').trigger('keydown', { key: 'Home' })
    expect(wrapper.get('[data-testid=video-provider-seedance]').attributes('aria-selected')).toBe('true')
  })

  it('edits an existing Muse account in its own tab without exposing its secret or changing its service', async () => {
    const wrapper = setup({ id: 8, name: 'Existing Muse', platform: 'openai', type: 'apikey', group_ids: [7],
      credentials: { api_key: 'must-not-display', base_url: 'https://muse.example.test/v1', openai_capabilities: ['muse'],
        model_mapping: { 'my-muse': 'muse-video' }, future_option: { keep: true } }, extra: { existing_setting: true }
    } as Account)
    expect(wrapper.get('[data-testid=video-provider-muse]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get<HTMLButtonElement>('[data-testid=video-provider-seedance]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-api-key]').element.value).toBe('')
    expect(wrapper.html()).not.toContain('must-not-display')
    await wrapper.get('[data-testid=video-provider-seedance]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(8, expect.objectContaining({ group_ids: [7], extra: { existing_setting: true }, credentials: {
      base_url: 'https://muse.example.test/v1', openai_capabilities: ['muse'], model_mapping: { 'my-muse': 'muse-video' }, future_option: { keep: true }
    } }))
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('rejects a Muse upstream model that would not route through the Muse protocol', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-provider-muse]').trigger('click')
    await wrapper.get('[data-testid=video-name]').setValue('Muse')
    await wrapper.get('[data-testid=video-api-key]').setValue('synthetic-muse-key')
    await wrapper.get('[data-testid=video-model-upstream-0]').setValue('other-video')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.videoAccount.museModelRequired')
  })

  it('creates a video-only account with documented models and selected resolution capabilities', async () => {
    const wrapper = setup()
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-base-url"]').element.value).toBe('https://api.newtoken.club')
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-resolution-720p"]').element.checked).toBe(true)
    expect(wrapper.findAll('[data-testid^="video-resolution-"]')).toHaveLength(4)
    expect(wrapper.findAll('[data-testid^="video-model-enabled-"]')).toHaveLength(5)
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-output-storage]').element.value).toBe('upstream')
    await wrapper.get('[data-testid="video-name"]').setValue('Video upstream')
    await wrapper.get('[data-testid="video-api-key"]').setValue('upstream-test-secret')
    await wrapper.get('[data-testid="video-resolution-1080p"]').setValue(true)
    wrapper.getComponent(GroupSelector).vm.$emit('update:modelValue', [7])
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.run).toHaveBeenCalledOnce()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Video upstream', platform: 'openai', type: 'apikey', group_ids: [7], extra: { seedance_output_storage: 'upstream' },
      credentials: {
        api_key: 'upstream-test-secret', base_url: 'https://api.newtoken.club', openai_capabilities: ['seedance'],
        seedance_allowed_resolutions: ['720p', '1080p'], model_mapping: Object.fromEntries(SEEDANCE_MODELS.map(model => [model.id, model.id])),
        seedance_model_specs: Object.fromEntries(SEEDANCE_MODELS.map(model => [model.id, model.id]))
      }
    }))
    expect(wrapper.emitted('created')).toHaveLength(1)
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-api-key"]').element.value).toBe('')
  })

  it('edits without displaying or resending an existing secret and preserves custom models and fields', async () => {
    const account = {
      id: 2, name: 'Existing video', platform: 'openai', type: 'apikey', group_ids: [7], concurrency: 3,
      proxy_id: 5, credentials: { api_key: 'must-not-display', base_url: 'https://api.newtoken.club',
        openai_capabilities: ['seedance'], seedance_allowed_resolutions: ['480p', '720p'],
        model_mapping: { 'custom-video': 'doubao-seedance-2-5-260628' }, future_option: { keep: true } }
    } as Account
    const wrapper = setup(account)
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-api-key"]').element.value).toBe('')
    expect(wrapper.html()).not.toContain('must-not-display')
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-resolution-480p"]').element.checked).toBe(true)
    await wrapper.get('[data-testid="video-resolution-1080p"]').setValue(true)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(2, expect.objectContaining({
      concurrency: 3, proxy_id: 5, group_ids: [7],
      credentials: { base_url: 'https://api.newtoken.club', openai_capabilities: ['seedance'],
        seedance_allowed_resolutions: ['480p', '720p', '1080p'], model_mapping: { 'custom-video': 'doubao-seedance-2-5-260628' }, seedance_model_specs: { 'custom-video': 'doubao-seedance-2-5-260628' }, future_option: { keep: true } }
    }))
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('supports the shared generated storage policy and preserves other account settings', async () => {
    const wrapper = setup({ id: 2, name: 'Stored video', platform: 'openai', type: 'apikey', extra: { seedance_output_storage: 'system', existing_setting: true }, credentials: { openai_capabilities: ['seedance'] } } as Account)
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-output-storage]').element.value).toBe('system')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(2, expect.objectContaining({ extra: { seedance_output_storage: 'system', existing_setting: true } }))
    await wrapper.get('[data-testid=video-output-storage]').setValue('upstream')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith(2, expect.objectContaining({ extra: { seedance_output_storage: 'upstream', existing_setting: true } }))
  })

  it('edits downstream and upstream aliases independently while retaining an official spec', async () => {
    const wrapper = setup({ id: 2, name: 'Mapped video', platform: 'openai', type: 'apikey', credentials: {
      openai_capabilities: ['seedance'], model_mapping: { 'my-video': 'vendor-old' },
      seedance_model_specs: { 'my-video': 'doubao-seedance-2-5-260628' }
    } } as Account)
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-downstream-0]').element.value).toBe('my-video')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-upstream-0]').element.value).toBe('vendor-old')
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-model-spec-0]').element.value).toBe('doubao-seedance-2-5-260628')
    await wrapper.get('[data-testid=video-model-downstream-0]').setValue(' public-video ')
    await wrapper.get('[data-testid=video-model-upstream-0]').setValue(' vendor-seedance-2.5 ')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(2, expect.objectContaining({ credentials: expect.objectContaining({
      model_mapping: { 'public-video': 'vendor-seedance-2.5' },
      seedance_model_specs: { 'public-video': 'doubao-seedance-2-5-260628' }
    }) }))
  })

  it('adds and removes mapping rows and excludes disabled rows from both maps', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-name]').setValue('Video')
    await wrapper.get('[data-testid=video-api-key]').setValue('secret')
    for (let index = 0; index < SEEDANCE_MODELS.length; index++) await wrapper.get(`[data-testid=video-model-enabled-${index}]`).setValue(false)
    await wrapper.get('[data-testid=video-model-add]').trigger('click')
    await wrapper.get(`[data-testid=video-model-downstream-${SEEDANCE_MODELS.length}]`).setValue('new-video')
    await wrapper.get(`[data-testid=video-model-upstream-${SEEDANCE_MODELS.length}]`).setValue('ep-upstream')
    await wrapper.get(`[data-testid=video-model-spec-${SEEDANCE_MODELS.length}]`).setValue('doubao-seedance-2-0-fast-260128')
    await wrapper.get('[data-testid=video-model-remove-0]').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ credentials: expect.objectContaining({
      model_mapping: { 'new-video': 'ep-upstream' }, seedance_model_specs: { 'new-video': 'doubao-seedance-2-0-fast-260128' }
    }) }))
  })

  it.each([
    ['downstream', '', 'invalidModelMapping'],
    ['upstream', '', 'invalidModelMapping'],
    ['downstream', 'video-*', 'invalidModelMapping'],
    ['upstream', 'vendor-*', 'invalidModelMapping'],
    ['downstream', 'doubao-seedance-2-0-fast-260128', 'duplicateModelMapping'],
    ['spec', '', 'selectModelSpec']
  ])('rejects invalid %s mapping %s before writing', async (field, value, message) => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-name]').setValue('Video')
    await wrapper.get('[data-testid=video-api-key]').setValue('secret')
    await wrapper.get(`[data-testid=video-model-${field}-0]`).setValue(value)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith(`admin.accounts.videoAccount.${message}`)
  })

  it('requires an official spec for legacy aliases that cannot be inferred', async () => {
    const wrapper = setup({ id: 2, name: 'Legacy alias', platform: 'openai', type: 'apikey', credentials: {
      openai_capabilities: ['seedance'], model_mapping: { 'my-video': 'ep-unknown' }
    } } as Account)
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-model-spec-0]').element.value).toBe('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.videoAccount.selectModelSpec')
    await wrapper.get('[data-testid=video-model-spec-0]').setValue('doubao-seedance-2-5-260628')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(2, expect.objectContaining({ credentials: expect.objectContaining({
      model_mapping: { 'my-video': 'ep-unknown' }, seedance_model_specs: { 'my-video': 'doubao-seedance-2-5-260628' }
    }) }))
  })

  it('requires at least one enabled mapping and ignores incomplete disabled drafts', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-name]').setValue('Video')
    await wrapper.get('[data-testid=video-api-key]').setValue('secret')
    for (let index = 0; index < SEEDANCE_MODELS.length; index++) await wrapper.get(`[data-testid=video-model-enabled-${index}]`).setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.videoAccount.selectModel')
    await wrapper.get('[data-testid=video-model-enabled-0]').setValue(true)
    await wrapper.get('[data-testid=video-model-downstream-1]').setValue('')
    await wrapper.get('[data-testid=video-model-spec-1]').setValue('')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ credentials: expect.objectContaining({
      model_mapping: { [SEEDANCE_MODELS[0]!.id]: SEEDANCE_MODELS[0]!.id }, seedance_model_specs: { [SEEDANCE_MODELS[0]!.id]: SEEDANCE_MODELS[0]!.id }
    }) }))
  })

  it('resets mapping drafts when switching edited accounts', async () => {
    const wrapper = setup({ id: 2, name: 'A', platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['seedance'], model_mapping: { alpha: 'vendor-a' }, seedance_model_specs: { alpha: 'doubao-seedance-2-5-260628' } } } as Account)
    await wrapper.get('[data-testid=video-model-downstream-0]').setValue('unsaved')
    await wrapper.setProps({ account: { id: 3, name: 'B', platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['seedance'], model_mapping: { beta: 'vendor-b' }, seedance_model_specs: { beta: 'doubao-seedance-2-0-fast-260128' } } } as Account })
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-downstream-0]').element.value).toBe('beta')
    expect(wrapper.get<HTMLInputElement>('[data-testid=video-model-upstream-0]').element.value).toBe('vendor-b')
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-model-spec-0]').element.value).toBe('doubao-seedance-2-0-fast-260128')
  })

  it('infers legacy official specs from upstream first, then downstream names', () => {
    const canonical = SEEDANCE_MODELS[0]!.id
    const upstream = SEEDANCE_MODELS[1]!.id
    expect(createSeedanceModelMappings({ model_mapping: { public: upstream, [canonical]: 'ep-unknown', unknown: 'vendor' } })).toEqual([
      { enabled: true, downstream: 'public', upstream, spec: upstream },
      { enabled: true, downstream: canonical, upstream: 'ep-unknown', spec: canonical },
      { enabled: true, downstream: 'unknown', upstream: 'vendor', spec: '' }
    ])
  })

  it('keeps Flash exclusive to NewToken without blocking older models', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid=video-name]').setValue('Other upstream')
    await wrapper.get('[data-testid=video-api-key]').setValue('test-key')
    await wrapper.get('[data-testid=video-base-url]').setValue('https://other.example')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.videoAccount.flashNewTokenOnly')
    expect(mocks.create).not.toHaveBeenCalled()
    const flash = SEEDANCE_MODELS.findIndex(model => model.id === 'doubao-seedance-2-5-flash-261115')
    await wrapper.get(`[data-testid=video-model-enabled-${flash}]`).setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.create).toHaveBeenCalledOnce()
  })

  it('rejects an empty resolution selection before writing', async () => {
    const wrapper = setup()
    await wrapper.get('[data-testid="video-name"]').setValue('Video')
    await wrapper.get('[data-testid="video-api-key"]').setValue('secret')
    await wrapper.get('[data-testid="video-resolution-720p"]').setValue(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.error).toHaveBeenCalledWith('admin.accounts.videoAccount.selectResolution')
  })

  it('initializes legacy unrestricted Seedance accounts with supported models and clears replacement keys on close', async () => {
    const wrapper = setup({ id: 3, name: 'Legacy', platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['seedance'] } } as Account)
    expect(wrapper.findAll<HTMLInputElement>('[data-testid^="video-model-enabled-"]').every(input => input.element.checked)).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-output-storage]').element.value).toBe('upstream')
    await wrapper.get('[data-testid="video-api-key"]').setValue('replacement-key')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-api-key"]').element.value).toBe('')
  })

  it('only routes dedicated video accounts to this editor', () => {
    const account = { platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['seedance'] } } as Account
    expect(isVideoAccount(account)).toBe(true)
    expect(isVideoAccount({ ...account, credentials: { openai_capabilities: ['muse'] } })).toBe(true)
    expect(isVideoAccount({ ...account, credentials: { openai_capabilities: ['muse', 'chat_completions'] } })).toBe(false)
    expect(isVideoAccount({ ...account, credentials: { openai_capabilities: ['seedance', 'muse'] } })).toBe(false)
    expect(isVideoAccount({ ...account, credentials: { openai_capabilities: ['seedance', 'chat_completions'] } })).toBe(false)
    expect(isVideoAccount({ ...account, platform: 'grok' })).toBe(false)
  })
})
