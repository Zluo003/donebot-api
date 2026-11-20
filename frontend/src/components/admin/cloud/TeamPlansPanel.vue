<template>
  <form class="mt-8 max-w-3xl space-y-4 border-t pt-6" @submit.prevent="save">
    <h3 class="font-semibold">团队 Cloud 独立套餐</h3>
    <p class="text-sm text-gray-500">团队由所有者付费。个人 Cloud 规格不受影响；已购买实例保留购买时的规格与价格。团队能力完成兼容验证后再开放购买。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <fieldset v-for="(plan, index) in plans" :key="plan.id" class="space-y-3 rounded-lg border p-4" :disabled="busy || !loaded">
      <legend class="px-2">{{ plan.name || `套餐 ${index + 1}` }}</legend>
      <label class="block text-sm">套餐名称<input v-model="plan.name" class="input mt-1" required maxlength="100"></label>
      <label class="flex items-center gap-2"><input v-model="plan.enabled" type="checkbox">启用套餐（需完成全部规格）</label>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">每月扣除余额金额<input v-model="plan.monthly_price" class="input mt-1" inputmode="decimal" required pattern="(0|[1-9][0-9]{0,6})(\.[0-9]{1,6})?"></label>
        <label class="text-sm">到期保留天数<input v-model.number="plan.retention_days" class="input mt-1" type="number" min="1" max="365" step="1" required></label>
      </div>
      <div v-for="part in parts" :key="part.key" class="space-y-2">
        <h4 class="text-sm font-medium">{{ part.label }}</h4>
        <div class="grid gap-2 sm:grid-cols-4"><label v-for="field in fields" :key="field.key" class="text-sm">{{ field.label }}<input v-model.number="plan[part.key][field.key]" class="input mt-1" type="number" :min="field.min" :max="field.max" step="1" required></label></div>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">共享文件总量（字节）<input v-model.number="plan.shared_quota_bytes" class="input mt-1" type="number" min="1" step="1" required></label>
        <label class="text-sm">单文件上限（字节）<input v-model.number="plan.max_file_bytes" class="input mt-1" type="number" min="1" step="1" required></label>
      </div>
      <p class="text-xs text-gray-500">共享文件配额占用运行盘，不增加额外磁盘。运行服务和桌面分别限制资源。</p>
      <button type="button" class="btn" @click="plans.splice(index, 1)">移除此套餐配置</button>
    </fieldset>
    <div class="flex gap-3"><button type="button" class="btn" :disabled="busy || !loaded || plans.length >= 20" @click="add">增加团队套餐</button><button type="submit" class="btn btn-primary" :disabled="busy || !loaded">保存团队套餐</button></div>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot, { type TeamCloudPlan, type TeamResources } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
const app = useAppStore(), stepUp = useStepUp()
const plans = ref<TeamCloudPlan[]>([]), busy = ref(false), loaded = ref(false), error = ref('')
const parts = [{ key: 'runtime', label: '运行服务' }, { key: 'desktop', label: '图形桌面' }] as const
const fields: Array<{ key: keyof TeamResources; label: string; min: number; max: number }> = [
  { key: 'cpu_millis', label: 'CPU（千分之一核）', min: 250, max: 32000 }, { key: 'memory_mib', label: '内存（MiB）', min: 512, max: 65536 },
  { key: 'storage_gib', label: '存储（GiB）', min: 1, max: 1024 }, { key: 'pids', label: '进程与线程', min: 64, max: 4096 }
]
onMounted(async () => {
  busy.value = true
  try { plans.value = await donebot.teamPlans(); loaded.value = true }
  catch { error.value = '团队套餐加载失败，请刷新重试。' } finally { busy.value = false }
})
function add() {
  plans.value.push({ id: crypto.randomUUID(), name: '', enabled: false, monthly_price: '0', retention_days: 7,
    runtime: { cpu_millis: 1000, memory_mib: 2048, storage_gib: 5, pids: 256 }, desktop: { cpu_millis: 1000, memory_mib: 2048, storage_gib: 10, pids: 256 }, shared_quota_bytes: 2 * 1024 ** 3, max_file_bytes: 100 * 1024 ** 2 })
}
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''
  try { plans.value = await stepUp.run(() => donebot.saveTeamPlans(plans.value)); app.showSuccess('团队套餐已保存') }
  catch (cause) { if (!isStepUpCancelled(cause)) error.value = '保存失败，请检查价格、资源和文件配额。' }
  finally { busy.value = false }
}
</script>
