import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { cloudAdminRoutes, redirectLegacyCloud } from '@/router/cloudAdminRoutes'
import donebot from '@/api/admin/donebot'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: defineComponent({ template: '<main><slot /></main>' }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot', () => ({ default: {
  cloudPlan: vi.fn(), cloudNodes: vi.fn(), cloudInstances: vi.fn(), cloudEvents: vi.fn(), cloudRuntimePolicy: vi.fn(), saveCloudRuntimePolicy: vi.fn(), saveCloudNode: vi.fn()
} }))

beforeEach(() => {
  setActivePinia(createPinia()); vi.resetAllMocks()
  vi.mocked(donebot.cloudPlan).mockResolvedValue({ enabled: true, monthly_price: '10', retention_days: 7 })
  vi.mocked(donebot.cloudNodes).mockResolvedValue({ nodes: [], public_key: 'synthetic-public-key' })
  vi.mocked(donebot.cloudInstances).mockResolvedValue([])
  vi.mocked(donebot.cloudEvents).mockResolvedValue([])
  vi.mocked(donebot.cloudRuntimePolicy).mockResolvedValue({ outage_grace_minutes: 30 })
})

async function fixture(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    ...cloudAdminRoutes,
    { path: '/admin/donebot', component: { template: '<div>DoneBot</div>' }, beforeEnter: redirectLegacyCloud }
  ] })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(defineComponent({ components: { RouterView }, template: '<RouterView />' }), { global: { plugins: [router] } })
  await flushPromises()
  return { router, wrapper }
}

describe('Cloud management navigation', () => {
  it('preserves legacy Cloud links and admin requirements on all six independent pages', async () => {
    const { router, wrapper } = await fixture('/admin/donebot?tab=cloud')
    expect(router.currentRoute.value.path).toBe('/admin/cloud/overview')
    const links = wrapper.findAll('nav[aria-label="Cloud 管理页面"] a')
    expect(links.map(a => a.text())).toEqual(['概览', '服务器节点', '用户实例', '套餐与保留', '账单', '运行策略'])
    for (const link of links) {
      const resolved = router.resolve(link.attributes('href'))
      expect(resolved.meta.requiresAdmin).toBe(true)
      expect(resolved.meta.requiresAuth).toBe(true)
    }
    await links[4].trigger('click'); await flushPromises()
    expect(router.currentRoute.value.path).toBe('/admin/cloud/billing')
    expect(wrapper.text()).toContain('最近 100 笔 Cloud 账单')
    expect(wrapper.find('form').exists()).toBe(false)
    wrapper.unmount()
  })

  it('loads only the requested management page and clears enrollment secrets on navigation', async () => {
    const { router, wrapper } = await fixture('/admin/cloud/nodes')
    expect(donebot.cloudInstances).not.toHaveBeenCalled()
    expect(donebot.cloudPlan).not.toHaveBeenCalled()
    expect(donebot.cloudEvents).not.toHaveBeenCalled()
    vi.mocked(donebot.saveCloudNode).mockResolvedValue({ node: { id: 'synthetic-node' }, token: 'synthetic-one-time-token', public_key: 'synthetic-public-key' })
    const form = wrapper.get('form')
    await form.findAll('input')[0].setValue('Synthetic node')
    await form.findAll('input')[1].setValue('https://node.example.test')
    await form.trigger('submit'); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[aria-label="节点令牌"]').element.value).toBe('synthetic-one-time-token')
    await router.push('/admin/cloud/instances'); await flushPromises()
    expect(wrapper.find('[aria-label="节点令牌"]').exists()).toBe(false)
    await router.push('/admin/cloud/nodes'); await flushPromises()
    expect(wrapper.find('[aria-label="节点令牌"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('saves an independent runtime policy and keeps writes disabled if its load fails', async () => {
    vi.mocked(donebot.saveCloudRuntimePolicy).mockImplementation(async p => p)
    const { router, wrapper } = await fixture('/admin/cloud/runtime-policy')
    await wrapper.get('[aria-label="最大失联运行分钟"]').setValue(45)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(donebot.saveCloudRuntimePolicy).toHaveBeenCalledWith({ outage_grace_minutes: 45 })
    await router.push('/admin/cloud/billing'); await flushPromises()
    vi.mocked(donebot.cloudRuntimePolicy).mockRejectedValue(new Error('offline'))
    await router.push('/admin/cloud/runtime-policy'); await flushPromises()
    expect(wrapper.get('[aria-label="最大失联运行分钟"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
