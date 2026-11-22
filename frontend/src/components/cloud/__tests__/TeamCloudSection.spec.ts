import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import TeamCloudSection from '../TeamCloudSection.vue'
import { teamsAPI, type TeamCloudPlan, type TeamCloudState } from '@/api/teams'

vi.mock('@/api/teams', () => ({
  teamsAPI: {
    workspaces: vi.fn(),
    plans: vi.fn(),
    create: vi.fn(),
    accept: vi.fn(),
    members: vi.fn(),
    invite: vi.fn(),
    revokeInvitation: vi.fn(),
    setMemberRole: vi.fn(),
    removeMember: vi.fn(),
    cloud: vi.fn(),
    mutateCloud: vi.fn(),
    launchBrowser: vi.fn(),
    launchDesktop: vi.fn()
  }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { balance: 100 }, refreshUser: vi.fn(async () => ({})) }) }))
vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: { props: ['title'], template: '<section role="dialog"><h2>{{ title }}</h2><slot /><slot name="footer" /></section>' }
}))

let wrapper: VueWrapper | undefined
const plan: TeamCloudPlan = {
  id: '11111111-1111-4111-8111-111111111111',
  name: 'Studio 团队版',
  enabled: true,
  monthly_price: '49',
  retention_days: 7,
  runtime: { cpu_millis: 2000, memory_mib: 4096, storage_gib: 10, pids: 256 },
  desktop: { cpu_millis: 1000, memory_mib: 2048, storage_gib: 10, pids: 512 },
  shared_quota_bytes: 5 * 1024 ** 3,
  max_file_bytes: 100 * 1024 ** 2
}
const runningInstance: TeamCloudState = {
  instance_id: 'f7bf1b5b-24c8-4d0b-a2cd-fc76b64ad319',
  desired: 'running',
  actual: 'running',
  generation: 1,
  error: '',
  paid_until: new Date(Date.now() + 86400000).toISOString(),
  subscription_state: 'active',
  purge_locked: false,
  auto_renew: true,
  plan_id: plan.id,
  plan_name: plan.name
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(teamsAPI.plans).mockResolvedValue([plan])
  vi.mocked(teamsAPI.members).mockResolvedValue([
    { user_id: 1, email: 'owner@example.com', role: 'owner', version: 1 },
    { user_id: 2, email: 'member@example.com', role: 'member', version: 1 }
  ])
  vi.mocked(teamsAPI.cloud).mockResolvedValue({ instance: null, operations: [], connection_available: true })
})
afterEach(() => { wrapper?.unmount() })

describe('TeamCloudSection', () => {
  it('stays completely hidden when team capability is disabled', async () => {
    vi.mocked(teamsAPI.workspaces).mockResolvedValue({ protocol: 'donebot.teams.v1', capabilities: [], workspaces: [] })
    wrapper = mount(TeamCloudSection)
    await flushPromises()
    expect(wrapper.text()).toBe('')
  })

  it('lets the team owner select a plan and confirm purchase with idempotency key', async () => {
    vi.mocked(teamsAPI.workspaces).mockResolvedValue({
      protocol: 'donebot.teams.v1',
      capabilities: ['identity', 'cloud_access'],
      workspaces: [{ id: '22222222-2222-4222-8222-222222222222', name: '设计组', role: 'owner', member_version: 1, created_at: new Date().toISOString() }]
    })
    vi.mocked(teamsAPI.mutateCloud).mockResolvedValue({
      instance: runningInstance,
      operation: { id: 'op-1', action: 'start', state: 'pending', error: '' }
    })
    wrapper = mount(TeamCloudSection)
    await flushPromises()
    expect(wrapper.text()).toContain('Studio 团队版')
    await wrapper.findAll('button').find(b => b.text() === '为团队购买 Cloud 云主机')!.trigger('click')
    expect(wrapper.get('[role="dialog"]').text()).toContain('49 / 月')
    expect(teamsAPI.mutateCloud).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(b => b.text() === '确认')!.trigger('click')
    await flushPromises()
    expect(teamsAPI.mutateCloud).toHaveBeenCalledWith('22222222-2222-4222-8222-222222222222', 'start', expect.any(String), plan.id)
  })

  it('hides purchase controls for non-owner members and lets them open a running team Cloud', async () => {
    vi.mocked(teamsAPI.workspaces).mockResolvedValue({
      protocol: 'donebot.teams.v1',
      capabilities: ['identity', 'cloud_access'],
      workspaces: [{ id: '22222222-2222-4222-8222-222222222222', name: '设计组', role: 'member', member_version: 1, created_at: new Date().toISOString() }]
    })
    vi.mocked(teamsAPI.cloud).mockResolvedValue({ instance: runningInstance, operations: [], connection_available: true })
    wrapper = mount(TeamCloudSection)
    await flushPromises()
    expect(wrapper.text()).not.toContain('购买')
    expect(wrapper.text()).not.toContain('取消自动续费')
    const openBtn = wrapper.findAll('button').find(b => b.text() === '打开团队云实例')!
    expect(openBtn.attributes('disabled')).toBeUndefined()
  })
})
