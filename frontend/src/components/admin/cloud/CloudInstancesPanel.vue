<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center gap-3">
      <input v-model="query" class="input max-w-sm" placeholder="搜索用户 ID、实例或节点" aria-label="搜索 Cloud 实例">
      <button class="btn btn-secondary" :disabled="busy" @click="load">刷新实例</button>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead><tr><th>用户 / 实例</th><th>节点</th><th>期望状态</th><th>实际状态</th><th>付费到期</th><th>删除状态</th></tr></thead>
        <tbody><tr v-for="instance in filtered" :key="instance.id"><td>{{ instance.user_id }} / {{ instance.id }}</td><td>{{ instance.node_id }}</td><td>{{ instance.desired }}</td><td>{{ instance.actual }} {{ instance.error }}</td><td>{{ instance.paid_until ? new Date(instance.paid_until).toLocaleString() : '未付费' }}</td><td>{{ instance.purge_locked ? '正在删除数据' : instance.desired === 'deleting' ? '等待删除' : '—' }}</td></tr></tbody>
      </table>
    </div>
    <p v-if="loaded && !filtered.length" class="text-sm text-gray-500">暂无匹配的实例。</p>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import donebot from '@/api/admin/donebot'
const instances = ref<Awaited<ReturnType<typeof donebot.cloudInstances>>>([])
const busy = ref(false), loaded = ref(false), error = ref(''), query = ref('')
const filtered = computed(() => instances.value.filter(i => `${i.user_id} ${i.id} ${i.node_id}`.toLowerCase().includes(query.value.trim().toLowerCase())))
async function load() {
  busy.value = true; error.value = ''
  try { instances.value = await donebot.cloudInstances(); loaded.value = true }
  catch { error.value = '实例加载失败，请刷新重试。' }
  finally { busy.value = false }
}
onMounted(load)
</script>
