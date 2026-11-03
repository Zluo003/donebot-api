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
  it('opens a dedicated APIMart panel', async () => {
    const wrapper = mount(AccountTableActions, { props: { loading: false } })
    await wrapper.get('[data-testid="create-apimart-account"]').trigger('click')
    expect(wrapper.emitted('create-apimart')).toHaveLength(1)
    expect(wrapper.emitted('create')).toBeUndefined()
  })
})
