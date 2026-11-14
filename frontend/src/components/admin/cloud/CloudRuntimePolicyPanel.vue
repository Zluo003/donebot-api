<template>
  <form class="max-w-xl space-y-4" @submit.prevent="save">
    <h2 class="font-semibold">主控失联容错</h2>
    <p class="text-sm text-gray-500">节点正常每 3 秒续租。主控暂时不可达时，已运行实例可在最后一次有效租约内继续工作，不会因一次超时停机。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <label class="block text-sm">最大失联运行时间（分钟）
      <input v-model.number="policy.outage_grace_minutes" class="input mt-2" type="number" min="1" max="60" step="1" required aria-label="最大失联运行分钟" :disabled="busy || !loaded">
    </label>
    <p class="text-sm text-gray-500">默认 30 分钟，可设 1–60 分钟。已知订阅到期仍按到期时间停止；收到明确停机、撤销或鉴权拒绝时不等待窗口结束。失联期间的新撤销最迟在原租约到期后生效，网络错误不会延长租约。</p>
    <p class="text-sm text-gray-500">保存后在下一次成功续租时生效。需同时更新主控、worker 和 agent；旧节点仍限制为 20 秒。此策略不提供跨节点迁移或自动故障切换。</p>
    <button class="btn btn-primary" :disabled="busy || !loaded" type="submit">保存运行策略</button>
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
const policy = ref({ outage_grace_minutes: 30 })
const loaded = ref(false), busy = ref(false), error = ref('')
onMounted(async () => {
  try { policy.value = await donebot.cloudRuntimePolicy(); loaded.value = true }
  catch { error.value = '运行策略加载失败，请刷新重试。' }
})
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''
  try {
    policy.value = await stepUp.run(() => donebot.saveCloudRuntimePolicy({ ...policy.value }))
    app.showSuccess('Cloud 运行策略已保存')
  } catch (cause) { if (!isStepUpCancelled(cause)) error.value = '保存失败，请填写 1–60 的整数后重试。' }
  finally { busy.value = false }
}
</script>
