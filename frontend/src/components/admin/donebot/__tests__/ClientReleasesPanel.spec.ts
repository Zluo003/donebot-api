import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AxiosProgressEvent } from 'axios'
import ClientReleasesPanel from '../ClientReleasesPanel.vue'
import donebot from '@/api/admin/donebot'

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/api/admin/donebot', () => ({ default: { clientReleases: vi.fn(), uploadClientRelease: vi.fn(), publishClientRelease: vi.fn() } }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(donebot.clientReleases).mockResolvedValue({ releases: [] })
  vi.mocked(donebot.uploadClientRelease).mockResolvedValue({ version: '0.1.0', status: 'draft', created_at: '', platforms: {} })
})

async function chooseFile(wrapper: VueWrapper, testID: string, name: string, size = 1024) {
  const file = new File(['fixture installer'], name)
  Object.defineProperty(file, 'size', { value: size })
  const input = wrapper.get<HTMLInputElement>(`[data-testid=${testID}]`)
  Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
  await input.trigger('change')
  return file
}

async function uploadFixture() {
  const wrapper = mount(ClientReleasesPanel)
  await flushPromises()
  await wrapper.get('[data-testid=release-version]').setValue('0.1.0')
  await chooseFile(wrapper, 'release-mac-dmg-file', 'DoneBot-0.1.0-arm64.dmg')
  await wrapper.get('[data-testid=upload-macos-dmg]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('client installer uploads', () => {
  it('explains the missing version before enabling upload for a selected file', async () => {
    const wrapper = mount(ClientReleasesPanel)
    await flushPromises()
    await chooseFile(wrapper, 'release-mac-dmg-file', 'DoneBot-0.1.0-arm64.dmg')
    expect(wrapper.get('[data-testid=upload-macos-dmg]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('请先填写版本号')
    await wrapper.get('[data-testid=release-version]').setValue('0.1.0')
    expect(wrapper.get('[data-testid=upload-macos-dmg]').attributes('disabled')).toBeUndefined()
  })

  it.each([
    ['windows-x64', 'release-windows-file', 'upload-windows', 'DoneBot-0.1.0-setup.exe'],
    ['macos-arm64-dmg', 'release-mac-dmg-file', 'upload-macos-dmg', 'DoneBot-0.1.0-arm64.dmg'],
    ['macos-arm64', 'release-mac-zip-file', 'upload-macos-zip', 'DoneBot-0.1.0-arm64.zip'],
  ])('submits %s with its version, platform, notes and selected file', async (platform, input, button, filename) => {
    const wrapper = mount(ClientReleasesPanel)
    await flushPromises()
    await wrapper.get('[data-testid=release-version]').setValue('v0.1.0')
    await wrapper.get('[data-testid=release-notes]').setValue('fixture release')
    const file = await chooseFile(wrapper, input, filename, 500_000_000)
    await wrapper.get(`[data-testid=${button}]`).trigger('click')
    await flushPromises()
    const form = vi.mocked(donebot.uploadClientRelease).mock.calls[0][0]
    expect(form.get('version')).toBe('0.1.0')
    expect(form.get('platform')).toBe(platform)
    expect(form.get('notes')).toBe('fixture release')
    expect(form.get('file')).toBe(file)
    expect(wrapper.get(`[data-testid=${button}]`).attributes('disabled')).toBeDefined()
    expect(donebot.publishClientRelease).not.toHaveBeenCalled()
  })

  it.each([0, 500_000_001])('rejects a %i-byte file before sending it', async size => {
    const wrapper = mount(ClientReleasesPanel)
    await flushPromises()
    await wrapper.get('[data-testid=release-version]').setValue('0.1.0')
    await chooseFile(wrapper, 'release-mac-dmg-file', 'fixture.dmg', size)
    expect(wrapper.get('[role=alert]').text()).toContain('不超过 500 MB')
    expect(wrapper.get('[data-testid=upload-macos-dmg]').attributes('disabled')).toBeDefined()
    expect(donebot.uploadClientRelease).not.toHaveBeenCalled()
  })

  it.each([
    [{ status: 500, message: 'Installer storage unavailable' }, 'Installer storage unavailable'],
    [{ status: 403, code: 'STEP_UP_TOTP_NOT_ENABLED', message: 'Enable two-factor authentication' }, 'Enable two-factor authentication'],
    [{ status: 413, message: 'Request Entity Too Large' }, 'HTTP 413'],
    [{ status: 0, code: 'ECONNABORTED' }, '最长等待 1 小时'],
    [{ status: 0, code: 'ERR_NETWORK' }, '上传连接中断'],
  ])('shows a useful error without automatically resubmitting: %j', async (failure, expected) => {
    vi.mocked(donebot.uploadClientRelease).mockRejectedValue(failure)
    const wrapper = await uploadFixture()
    expect(wrapper.get('[role=alert]').text()).toContain(expected)
    expect(donebot.uploadClientRelease).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid=upload-macos-dmg]').attributes('disabled')).toBeUndefined()
  })

  it('keeps the same multipart form when retrying after step-up verification', async () => {
    vi.mocked(donebot.uploadClientRelease).mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = await uploadFixture()
    const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
    expect(controller.visible.value).toBe(true)
    controller.onVerified()
    await flushPromises()
    const calls = vi.mocked(donebot.uploadClientRelease).mock.calls
    expect(calls).toHaveLength(2)
    expect(calls[0][0]).toBe(calls[1][0])
    expect(wrapper.find('[role=alert]').exists()).toBe(false)
  })

  it('distinguishes file transfer progress from server persistence', async () => {
    let finish!: () => void
    vi.mocked(donebot.uploadClientRelease).mockImplementation(async (_, progress) => {
      progress?.({ loaded: 50, total: 100 } as AxiosProgressEvent)
      await new Promise<void>(resolve => { finish = resolve })
      return { version: '0.1.0', status: 'draft', created_at: '', platforms: {} }
    })
    const wrapper = await uploadFixture()
    expect(wrapper.get('[role=status]').text()).toContain('50%')
    expect(wrapper.get('[data-testid=release-version]').attributes('disabled')).toBeDefined()
    const progress = vi.mocked(donebot.uploadClientRelease).mock.calls[0][1]
    progress?.({ loaded: 100, total: 100 } as AxiosProgressEvent)
    await flushPromises()
    expect(wrapper.get('[role=status]').text()).toContain('正在保存安装包')
    finish()
    await flushPromises()
    expect(wrapper.find('[role=status]').exists()).toBe(false)
  })
})
