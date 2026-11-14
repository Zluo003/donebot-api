<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between"><h2 class="font-semibold">最近 100 笔 Cloud 账单</h2><button class="btn btn-secondary" :disabled="busy" @click="load">刷新账单</button></div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th>时间</th><th>用户</th><th>实例</th><th>账期</th><th>类型</th><th>金额</th><th>余额</th></tr></thead>
      <tbody><tr v-for="event in events" :key="`${event.instance_id}-${event.cycle}-${event.event}`"><td>{{ new Date(event.created_at).toLocaleString() }}</td><td>{{ event.user_id }}</td><td>{{ event.instance_id }}</td><td>{{ event.cycle }}</td><td>{{ event.event === 'refund' ? '退款' : '扣款' }}</td><td>{{ event.amount }}</td><td>{{ event.balance_after }}</td></tr></tbody>
    </table></div>
    <p v-if="loaded && !events.length" class="text-sm text-gray-500">暂无 Cloud 账单。</p>
  </div>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot from '@/api/admin/donebot'
const events = ref<Awaited<ReturnType<typeof donebot.cloudEvents>>>([])
const busy = ref(false), loaded = ref(false), error = ref('')
async function load() {
  busy.value = true; error.value = ''
  try { events.value = await donebot.cloudEvents(); loaded.value = true }
  catch { error.value = 'Cloud 账单加载失败，请刷新重试。' }
  finally { busy.value = false }
}
onMounted(load)
</script>
