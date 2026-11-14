<template>
  <div class="space-y-5">
    <button class="btn btn-secondary" :disabled="busy" @click="load">刷新概览</button>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-if="loaded">
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="item in metrics" :key="item.label" class="rounded-lg border border-gray-200 p-4 dark:border-gray-700"><div class="text-sm text-gray-500">{{ item.label }}</div><div class="mt-1 text-2xl font-semibold">{{ item.value }}</div></div>
      </div>
      <p class="text-sm text-gray-500">月付开通：{{ plan.enabled ? '已启用' : '已关闭' }} · 月费 {{ plan.monthly_price }} · 到期后保留 {{ plan.retention_days }} 天 · 失联运行窗口 {{ policy.outage_grace_minutes }} 分钟</p>
      <h2 class="font-semibold">需要关注</h2>
      <ul v-if="attention.length" class="space-y-2 text-sm"><li v-for="item in attention" :key="item">{{ item }}</li></ul>
      <p v-else class="text-sm text-gray-500">本次刷新未发现节点离线或实例错误。</p>
      <p class="text-sm text-gray-500">节点在线按最近 30 秒心跳判断，统计截至本次刷新。暂停分配只阻止新实例；离线实例仍归属原节点，不会自动迁移。</p>
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import donebot from '@/api/admin/donebot'
const nodes = ref<Awaited<ReturnType<typeof donebot.cloudNodes>>['nodes']>([])
const instances = ref<Awaited<ReturnType<typeof donebot.cloudInstances>>>([])
const plan = ref({ enabled: false, monthly_price: '0', retention_days: 7 }), policy = ref({ outage_grace_minutes: 30 })
const busy = ref(false), loaded = ref(false), error = ref(''), refreshedAt = ref(0)
const online = computed(() => nodes.value.filter(n => n.last_seen && refreshedAt.value - Date.parse(n.last_seen) < 30000))
const metrics = computed(() => [
  { label: '在线 / 全部节点', value: `${online.value.length} / ${nodes.value.length}` },
  { label: '已分配实例', value: instances.value.length },
  { label: '实际运行中', value: instances.value.filter(i => i.actual === 'running').length },
  { label: '在线可分配名额', value: online.value.filter(n => n.enabled).reduce((sum, n) => sum + Math.max(0, n.capacity - n.assigned), 0) }
])
const attention = computed(() => [
  ...nodes.value.filter(n => !online.value.includes(n)).map(n => `节点 ${n.name} 未连接或已离线，已分配 ${n.assigned} 个实例。`),
  ...instances.value.filter(i => i.error || i.actual === 'error').map(i => `用户 ${i.user_id} / ${i.id}：${i.error || i.actual}`)
])
async function load() {
  busy.value = true; error.value = ''
  try {
    const [n, i, p, r] = await Promise.all([donebot.cloudNodes(), donebot.cloudInstances(), donebot.cloudPlan(), donebot.cloudRuntimePolicy()])
    nodes.value = n.nodes; instances.value = i; plan.value = p; policy.value = r; refreshedAt.value = Date.now(); loaded.value = true
  } catch { error.value = 'Cloud 概览加载失败，请刷新重试。' }
  finally { busy.value = false }
}
onMounted(load)
</script>
