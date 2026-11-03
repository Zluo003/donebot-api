import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ImageTaskLookup from '../ImageTaskLookup.vue'
import donebot from '@/api/admin/donebot'
vi.mock('@/api/admin/donebot', () => ({ default: { imageTask: vi.fn() } }))
describe('image task installation lookup', () => {
 it('shows task status, user and installation without exposing credentials', async () => {
  vi.mocked(donebot.imageTask).mockResolvedValue({task_id:'imgtask_123',status:'completed',user_id:3,install:{id:12,name:'Mac'},created_at:1,expires_at:2})
  const wrapper=mount(ImageTaskLookup)
  await wrapper.get('input').setValue('imgtask_123')
  await wrapper.get('form').trigger('submit');await flushPromises()
  expect(donebot.imageTask).toHaveBeenCalledWith('imgtask_123')
  expect(wrapper.text()).toContain('Mac');expect(wrapper.text()).toContain('completed');expect(wrapper.text()).toContain('用户 3')
 })
})
