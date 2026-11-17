<template>
  <div class="space-y-4">
    <p class="text-sm text-gray-500">编程执行器使用独立原生分组。费用按现有模型价格与分组倍率计入同一账户余额。</p>
    <div v-for="group in groups" :key="group.id" class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 class="font-semibold">{{ group.name }}</h2>
          <p class="text-sm text-gray-500">{{ group.engine === 'codex' ? '/v1/responses' : '/v1/messages' }} · {{ group.status }}</p>
        </div>
        <RouterLink class="text-sm text-primary-600" to="/admin/groups">管理模型、渠道与分组</RouterLink>
      </div>
      <form class="mt-4 flex flex-wrap items-end gap-3" @submit.prevent="save(group)">
        <label class="text-sm">分组倍率（1 为原价，0.8 为八折）
          <input v-model.number="group.rate_multiplier" class="input mt-1 block w-48" type="number" min="0" step="0.01" required />
        </label>
        <button class="btn btn-primary" type="submit" :disabled="saving === group.id">保存倍率</button>
      </form>
    </div>
    <p v-if="!groups.length" class="text-sm text-gray-500">{{ loading ? '正在加载编程分组…' : '编程分组尚未配置，请检查网关启动配置。' }}</p>
    <p class="text-sm text-gray-500">新分组默认不开放模型。请在分组管理中绑定相应平台的上游账号，并配置模型白名单。</p>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import donebot, { type CodingGroup } from '@/api/admin/donebot'
import { update } from '@/api/admin/groups'
import { useAppStore } from '@/stores/app'
const app = useAppStore()
const groups = ref<CodingGroup[]>([])
const loading = ref(true)
const saving = ref<number | null>(null)
onMounted(async () => {
  try { groups.value = await donebot.codingGroups() }
  catch (error) { app.showError(error instanceof Error ? error.message : '加载编程分组失败') }
  finally { loading.value = false }
})
async function save(group: CodingGroup) {
  if (!Number.isFinite(group.rate_multiplier) || group.rate_multiplier < 0) { app.showError('请输入有效倍率'); return }
  saving.value = group.id
  try {
    const saved = await update(group.id, { rate_multiplier: group.rate_multiplier })
    group.rate_multiplier = saved.rate_multiplier
    app.showSuccess('分组倍率已保存')
  } catch (error) { app.showError(error instanceof Error ? error.message : '保存倍率失败') }
  finally { saving.value = null }
}
</script>
