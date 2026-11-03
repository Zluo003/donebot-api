<template>
  <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-gray-700">
    <h2 class="text-base font-semibold">图片任务归属</h2>
    <form class="flex flex-wrap gap-3" @submit.prevent="lookup">
      <label class="min-w-0 flex-1">
        <span class="sr-only">任务 ID</span>
        <input v-model="taskID" class="input" placeholder="输入 imgtask_… 查询提交安装" :disabled="busy" required>
      </label>
      <button class="btn btn-secondary" :disabled="busy || !taskID.trim()">查询任务</button>
    </form>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <div v-if="task" class="space-y-1 text-sm">
      <p class="break-all font-medium">{{ task.task_id }} · {{ task.status }}</p>
      <p>安装：{{ task.install.name }}（ID {{ task.install.id }}） · 用户 {{ task.user_id }}</p>
    </div>
  </section>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import donebot, { type AdminImageTask } from '@/api/admin/donebot'
const taskID = ref('')
const task = ref<AdminImageTask | null>(null)
const busy = ref(false)
const error = ref('')
async function lookup() {
  busy.value = true
  error.value = ''
  task.value = null
  try {
    task.value = await donebot.imageTask(taskID.value.trim())
  } catch (err) {
    error.value = err && typeof err === 'object' && 'message' in err ? String(err.message) : '任务不存在或不属于 DoneBot 安装'
  } finally {
    busy.value = false
  }
}
</script>
