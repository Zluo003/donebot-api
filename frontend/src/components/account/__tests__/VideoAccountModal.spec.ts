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
import { isSeedanceVideoAccount, SEEDANCE_MODELS } from '../videoAccount'

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

describe('Seedance video account panel', () => {
  it('creates a video-only account with documented models and selected resolution capabilities', async () => {
    const wrapper = setup()
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-base-url"]').element.value).toBe('https://api.newtoken.club')
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-resolution-720p"]').element.checked).toBe(true)
    expect(wrapper.findAll('[data-testid^="video-resolution-"]')).toHaveLength(4)
    expect(wrapper.findAll('[data-testid^="video-model-"]')).toHaveLength(4)
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
        seedance_allowed_resolutions: ['720p', '1080p'], model_mapping: Object.fromEntries(SEEDANCE_MODELS.map(model => [model.id, model.id]))
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
        seedance_allowed_resolutions: ['480p', '720p', '1080p'], model_mapping: { 'custom-video': 'doubao-seedance-2-5-260628' }, future_option: { keep: true } }
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
    expect(wrapper.findAll<HTMLInputElement>('[data-testid^="video-model-"]').every(input => input.element.checked)).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-testid=video-output-storage]').element.value).toBe('upstream')
    await wrapper.get('[data-testid="video-api-key"]').setValue('replacement-key')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get<HTMLInputElement>('[data-testid="video-api-key"]').element.value).toBe('')
  })

  it('only routes dedicated Seedance accounts to this editor', () => {
    const account = { platform: 'openai', type: 'apikey', credentials: { openai_capabilities: ['seedance'] } } as Account
    expect(isSeedanceVideoAccount(account)).toBe(true)
    expect(isSeedanceVideoAccount({ ...account, credentials: { openai_capabilities: ['seedance', 'chat_completions'] } })).toBe(false)
    expect(isSeedanceVideoAccount({ ...account, platform: 'grok' })).toBe(false)
  })
})
