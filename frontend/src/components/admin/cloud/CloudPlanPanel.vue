<template>
  <form class="max-w-xl space-y-4" @submit.prevent="save">
    <h3 class="font-semibold">Cloud 按月预付</h3>
    <p class="text-sm text-gray-500">从用户现有账户余额扣除月费，成功后开通一个月。余额不足不续期，到期停止实例并保留数据。价格调整在下次购买或续费时生效。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <label class="flex items-center gap-2"><input v-model="plan.enabled" type="checkbox" :disabled="busy || !loaded">启用开通和自动续费</label>
    <label class="block text-sm">每月扣除余额金额（与账户余额同单位）
      <input v-model="plan.monthly_price" class="input mt-2" inputmode="decimal" aria-label="Cloud 月费" :disabled="busy || !loaded" required pattern="(0|[1-9][0-9]{0,6})(\.[0-9]{1,6})?">
    </label>
    <label class="block text-sm">到期未续费后保留数据（天）
      <input v-model.number="plan.retention_days" class="input mt-2" type="number" min="1" max="365" step="1" aria-label="Cloud 到期保留天数" :disabled="busy || !loaded" required>
    </label>
    <button class="btn btn-primary" type="submit" :disabled="busy || !loaded">保存 Cloud 月费</button>
    <p class="text-xs text-gray-500">到期立即停机；超过保留期限后自动永久删除实例和专属数据。节点离线或删除失败时保持待处理状态。调整保留天数会应用于已有过期实例。关闭收费后不再扣取新的月费，已支付账期继续有效。停止实例不等于取消订阅；尚未成功开通的订阅取消后退回本期扣款。</p>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
const app = useAppStore(), stepUp = useStepUp()
const plan = ref({ enabled: false, monthly_price: '0', retention_days: 7 })
const busy = ref(false), loaded = ref(false), error = ref('')
onMounted(async () => {
  busy.value = true
  try { plan.value = await donebot.cloudPlan(); loaded.value = true }
  catch { error.value = 'Cloud 套餐加载失败，请刷新重试。' } finally { busy.value = false }
})
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''
  try { plan.value = await stepUp.run(() => donebot.saveCloudPlan({ ...plan.value })); app.showSuccess('Cloud 月费已保存') }
  catch (cause) { if (!isStepUpCancelled(cause)) error.value = '保存失败，请检查金额后重试。' }
  finally { busy.value = false }
}
</script>
