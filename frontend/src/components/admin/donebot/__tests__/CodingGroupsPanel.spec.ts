import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'
import CodingGroupsPanel from '../CodingGroupsPanel.vue'
import donebot from '@/api/admin/donebot'
import { update } from '@/api/admin/groups'
const notices = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notices }))
vi.mock('@/api/admin/donebot', () => ({ default: { codingGroups: vi.fn() } }))
vi.mock('@/api/admin/groups', () => ({ update: vi.fn() }))
vi.mock('vue-router', () => ({ RouterLink: { template: '<a><slot /></a>' } }))
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(donebot.codingGroups).mockResolvedValue([
    { id: 71, engine: 'codex', name: 'DoneBot Codex', platform: 'openai', status: 'active', rate_multiplier: 0.8 },
    { id: 92, engine: 'claude', name: 'DoneBot Claude Code', platform: 'anthropic', status: 'active', rate_multiplier: 0.6 }
  ])
})
it('saves each native group multiplier through standard group management', async () => {
  const wrapper = mount(CodingGroupsPanel)
  await flushPromises()
  const forms = wrapper.findAll('form')
  for (const [index, id, rate] of [[0, 71, 0.7], [1, 92, 0.4]] as const) {
    vi.mocked(update).mockResolvedValue({ id, rate_multiplier: rate } as Awaited<ReturnType<typeof update>>)
    await forms[index].find('input').setValue(rate)
    await forms[index].trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenLastCalledWith(id, { rate_multiplier: rate })
  }
  expect(update).toHaveBeenCalledTimes(2)
  expect(wrapper.text()).toContain('/v1/responses')
  expect(wrapper.text()).toContain('/v1/messages')
})
it('reports failed saving without announcing a changed billing rate', async () => {
  vi.mocked(update).mockRejectedValue(new Error('synthetic group save failure'))
  const wrapper = mount(CodingGroupsPanel)
  await flushPromises()
  await wrapper.find('form').trigger('submit')
  await flushPromises()
  expect(notices.showError).toHaveBeenCalledWith('synthetic group save failure')
  expect(notices.showSuccess).not.toHaveBeenCalled()
  expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
})
