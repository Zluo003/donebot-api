import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({ versions: vi.fn(), fetchVersion: vi.fn() }))
vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true }),
  useAppStore: () => ({ versionLoading: false, currentVersion: '1.0.1', latestVersion: '1.0.1', hasUpdate: false,
    releaseInfo: null, buildType: 'release', fetchVersion: mocks.fetchVersion })
}))
vi.mock('@/api/admin/system', () => ({
  getRollbackVersions: mocks.versions, performUpdate: vi.fn(), restartService: vi.fn(), rollback: vi.fn()
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() }) }))
import VersionBadge from '../VersionBadge.vue'

describe('DoneBot release rollback commands', () => {
  beforeEach(() => {
    mocks.versions.mockResolvedValue({ versions: [{ version: '1.0.0', published_at: '2026-11-03T00:00:00Z', html_url: '' }] })
    vi.clearAllMocks()
  })

  it('uses the matching repository tag and image for the selected version', async () => {
    const wrapper = mount(VersionBadge, { global: { stubs: { Icon: true } } })
    await wrapper.get('button').trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'version.rollback')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('v1.0.0'))!.trigger('click')
    expect(wrapper.text()).toContain('https://raw.githubusercontent.com/Zluo003/donebot-api/v1.0.0/deploy/install.sh')
    expect(wrapper.text()).toContain('rollback v1.0.0')
    await wrapper.findAll('button').find(button => button.text() === 'version.deployDocker')!.trigger('click')
    expect(wrapper.text()).toContain('image: ghcr.io/zluo003/donebot-api:1.0.0')
    wrapper.unmount()
  })
})
