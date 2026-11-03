import { ref, onMounted } from 'vue'
import donebot, { type Modality, type ModelsResult, type PricingInput } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'

export function useDoneBotModels(kind: Modality) {
  const state = ref<ModelsResult | null>(null)
  const busy = ref(false)
  const error = ref('')
  const app = useAppStore()

  async function run(action: () => Promise<ModelsResult>, message = '') {
    busy.value = true
    error.value = ''
    try {
      state.value = await action()
      if (message) app.showSuccess(message)
    } catch (err) {
      error.value = err instanceof Error ? err.message : '操作失败，请重试'
      app.showError(error.value)
    } finally {
      busy.value = false
    }
  }

  const load = () => run(() => donebot.getModels(kind))
  const sync = () => run(() => donebot.sync(kind), '模型已同步，已有开关保持不变')
  const saveModels = () => run(
    () => donebot.models(kind, state.value?.models.map(({ model, enabled }) => ({ model, enabled })) ?? []),
    '模型开关已保存'
  )
  const savePricing = (input: PricingInput) => run(() => donebot.pricing(kind, input), '价格配置已保存')
  onMounted(load)
  return { state, busy, error, load, sync, saveModels, savePricing }
}
