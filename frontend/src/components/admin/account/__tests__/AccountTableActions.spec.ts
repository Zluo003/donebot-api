import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountTableActions from '../AccountTableActions.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('account creation actions', () => {
  it('offers a dedicated video account action alongside ordinary account creation', async () => {
    const wrapper = mount(AccountTableActions, { props: { loading: false } })
    await wrapper.get('[data-testid="create-video-account"]').trigger('click')
    expect(wrapper.emitted('create-video')).toHaveLength(1)
    expect(wrapper.emitted('create')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.accounts.createAccount')
  })
  it.each(['image', 'music', 'tts'])('opens the separate APIMart %s entry', async kind => {
    const wrapper = mount(AccountTableActions, { props: { loading: false } })
    expect(wrapper.find('[data-testid="create-apimart-account"]').exists()).toBe(false)
    await wrapper.get(`[data-testid="create-apimart-${kind}-account"]`).trigger('click')
    expect(wrapper.emitted('create-apimart')).toEqual([[kind]])
    expect(wrapper.text()).toContain(`admin.accounts.apimartAccount.services.${kind}.createTitle`)
    expect(wrapper.emitted('create')).toBeUndefined()
  })
})
