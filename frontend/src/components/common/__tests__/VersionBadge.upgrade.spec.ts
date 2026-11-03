import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  auth: { isAdmin: true },
  app: {
    versionLoading: false,
    currentVersion: '0.3.0',
    latestVersion: '0.4.0',
    hasUpdate: true,
    releaseInfo: { html_url: 'https://github.com/Zluo003/donebot-api/releases/tag/v0.4.0' },
    buildType: 'release',
    fetchVersion: vi.fn(),
    clearVersionCache: vi.fn()
  },
  update: vi.fn(),
  restart: vi.fn()
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => mocks.auth,
  useAppStore: () => mocks.app
}))
vi.mock('@/api/admin/system', () => ({
  performUpdate: mocks.update,
  restartService: mocks.restart,
  getRollbackVersions: vi.fn(),
  rollback: vi.fn()
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() })
}))

import VersionBadge from '../VersionBadge.vue'

describe('admin online release upgrade', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.auth.isAdmin = true
    mocks.app.buildType = 'release'
    mocks.update.mockResolvedValue({ need_restart: true, message: 'ok' })
    mocks.restart.mockResolvedValue({ message: 'ok' })
  })

  afterEach(() => {
    vi.clearAllTimers()
    vi.useRealTimers()
  })

  it('offers the release upgrade and then allows restarting the installed binary', async () => {
    vi.useFakeTimers()
    const wrapper = mount(VersionBadge)
    await wrapper.get('button').trigger('click')
    const updateButton = wrapper.findAll('button').find(button => button.text() === 'version.updateNow')
    expect(updateButton).toBeDefined()
    await updateButton!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledOnce()
    expect(mocks.app.clearVersionCache).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('version.updateComplete')
    await wrapper.findAll('button').find(button => button.text() === 'version.restartNow')!.trigger('click')
    await flushPromises()
    expect(mocks.restart).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('version.restarting')
    wrapper.unmount()
  })

  it('shows a failed update without offering a restart', async () => {
    mocks.update.mockRejectedValue({ response: { data: { message: 'checksum verification failed' } } })
    const wrapper = mount(VersionBadge)
    await wrapper.get('button').trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'version.updateNow')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('checksum verification failed')
    expect(wrapper.text()).not.toContain('version.restartNow')
    expect(mocks.app.clearVersionCache).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows the source build instructions without an online binary replacement button', async () => {
    mocks.app.buildType = 'source'
    const wrapper = mount(VersionBadge)
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).toContain('version.sourceModeHint')
    expect(wrapper.text()).not.toContain('version.updateNow')
    expect(wrapper.get('a').attributes('href')).toBe(mocks.app.releaseInfo.html_url)
    wrapper.unmount()
  })

  it('limits online upgrade controls and update checks to administrators', () => {
    mocks.auth.isAdmin = false
    const wrapper = mount(VersionBadge)
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(mocks.app.fetchVersion).not.toHaveBeenCalled()
    expect(mocks.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
