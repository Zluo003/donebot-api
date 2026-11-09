import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, it, vi } from 'vitest'
import View from '../DoneBotTemplatesView.vue'
import library, { type TemplateRelease } from '@/api/admin/donebot-templates'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: defineComponent({ template: '<main><slot /></main>' }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: defineComponent({ name: 'TotpStepUpDialog', props: ['controller'], template: '<div />' }) }))
vi.mock('@/api/admin/donebot-templates', () => ({ default: { list: vi.fn(), preview: vi.fn(), publish: vi.fn(), get: vi.fn(), visibility: vi.fn() } }))
const entry: TemplateRelease = { id: 'commerce', name: '店铺运营', release: '1.0.0', summary: '商品与日常运营', category: '电商', kind: 'bot', members: ['运营'], skills: ['commerce'], bytes: 500, sha256: 'a'.repeat(64), published: true, created_at: '', document: { format: 'openmaus.package', version: 2, package: { id: 'commerce', name: '店铺运营', release: '1.0.0', summary: '商品与日常运营' } } }
beforeEach(() => {
  setActivePinia(createPinia()); vi.clearAllMocks()
  vi.mocked(library.list).mockResolvedValue([structuredClone(entry)])
  vi.mocked(library.preview).mockResolvedValue(structuredClone(entry))
  vi.mocked(library.publish).mockResolvedValue(structuredClone(entry))
  vi.mocked(library.get).mockResolvedValue(structuredClone(entry))
})
const button = (wrapper: ReturnType<typeof mount>, label: string) => wrapper.findAll('button').find(item => item.text() === label)!

it('uploads, previews, edits metadata and publishes the complete document', async () => {
  const wrapper = mount(View, { global: { stubs: { teleport: true } } }); await flushPromises()
  await button(wrapper, '发布模板').trigger('click')
  const input = wrapper.get<HTMLInputElement>('input[type=file]')
  Object.defineProperty(input.element, 'files', { value: [{ size: 400, text: async () => JSON.stringify(entry.document) }] })
  await input.trigger('change'); await flushPromises()
  await wrapper.get('[aria-label="模板名称"]').setValue('新运营')
  await wrapper.get('[aria-label="模板版本"]').setValue('1.0.1')
  await button(wrapper, '发布').trigger('click'); await flushPromises()
  expect(library.publish).toHaveBeenCalledWith({ ...entry.document, package: { ...entry.document.package, name: '新运营', release: '1.0.1' } })
  wrapper.unmount()
})
it('edits an existing template as a new version and unpublishes with step-up', async () => {
  const wrapper = mount(View, { global: { stubs: { teleport: true } } }); await flushPromises()
  await button(wrapper, '查看').trigger('click'); await flushPromises()
  expect(wrapper.get<HTMLInputElement>('[aria-label="模板名称"]').element.disabled).toBe(true)
  await button(wrapper, '发布新版本').trigger('click')
  expect(wrapper.get<HTMLInputElement>('[aria-label="模板版本"]').element.value).toBe('1.0.1')
  await button(wrapper, '关闭').trigger('click')
  vi.mocked(library.visibility).mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce(undefined)
  await button(wrapper, '下架').trigger('click'); await flushPromises()
  const controller = wrapper.getComponent({ name: 'TotpStepUpDialog' }).props('controller')
  expect(controller.visible.value).toBe(true)
  controller.onVerified(); await flushPromises()
  expect(library.visibility).toHaveBeenCalledTimes(2)
  expect(library.visibility).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'commerce' }), false)
  wrapper.unmount()
})
it('keeps a failed publication open and shows server validation errors', async () => {
  const wrapper = mount(View, { global: { stubs: { teleport: true } } }); await flushPromises()
  await button(wrapper, '查看').trigger('click'); await flushPromises()
  await button(wrapper, '发布新版本').trigger('click')
  vi.mocked(library.publish).mockRejectedValue({ response: { data: { message: '此版本已存在' } } })
  await button(wrapper, '发布').trigger('click'); await flushPromises()
  expect(wrapper.get('[role=alert]').text()).toBe('此版本已存在')
  expect(wrapper.get<HTMLInputElement>('[aria-label="模板名称"]').element.value).toBe('店铺运营')
  wrapper.unmount()
})
