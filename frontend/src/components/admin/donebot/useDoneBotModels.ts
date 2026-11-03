import { ref, onMounted } from 'vue'
import donebot, { type Modality, type ModelsResult, type ModelConfigurationInput } from '@/api/admin/donebot'
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
  const saveConfiguration = (input: ModelConfigurationInput) => run(() => donebot.saveConfiguration(kind, input), '模型配置已保存')
  onMounted(load)
  return { state, busy, error, load, sync, saveConfiguration }
}
