import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import RelayNodesPanel from '../RelayNodesPanel.vue'
import { relayAPI, type RelayNodeInput } from '@/api/admin/relay'

vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/relay', () => ({ relayAPI: { list: vi.fn(), save: vi.fn(), rotate: vi.fn() } }))

let wrapper: ReturnType<typeof mount>
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(relayAPI.list).mockResolvedValue({ nodes: [{ id: 'relay1', name: '主中继', endpoint: 'donebot.cn/mobile-relay', enabled: true, max_sessions: 1000, max_hosts: 1000, max_sockets: 3256, hosts: 0, sessions: 0, sockets: 0, last_seen: null }], token_keys: '{}', signing_configured: true })
})
afterEach(() => { wrapper?.unmount(); vi.restoreAllMocks() })

describe('relay node protected changes', () => {
  it('retries the original node settings after step-up and shows the one-time credential', async () => {
    const sent: RelayNodeInput[] = []
    vi.mocked(relayAPI.save).mockImplementation(async input => {
      sent.push({ ...input })
      if (sent.length === 1) throw { code: 'STEP_UP_REQUIRED' }
      return { token: 'synthetic-token', token_keys: '{}' }
    })
    wrapper = mount(RelayNodesPanel); await flushPromises()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
    expect(controller.visible.value).toBe(true)
    await wrapper.get('input[placeholder="relay1"]').setValue('relay2')
    controller.onVerified(); await flushPromises()
    expect(sent).toHaveLength(2)
    expect(sent[1]).toEqual(sent[0])
    expect(wrapper.get<HTMLTextAreaElement>('[aria-label="relay.env 配置"]').element.value).toContain('RELAY_NODE=relay1\nRELAY_NODE_TOKEN=synthetic-token')
  })

  it('cancels credential rotation without retrying or showing an error', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    vi.mocked(relayAPI.rotate).mockRejectedValue({ code: 'STEP_UP_REQUIRED' })
    wrapper = mount(RelayNodesPanel); await flushPromises()
    await wrapper.get('td button.block').trigger('click'); await flushPromises()
    const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
    expect(controller.visible.value).toBe(true)
    controller.onCancel(); await flushPromises()
    expect(relayAPI.rotate).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[aria-label="节点部署凭据"]').exists()).toBe(false)
  })
})
