<template>
  <AppLayout>
    <div class="min-w-0 space-y-6">
      <div>
        <h1 class="text-2xl font-bold">DoneBot</h1>
        <p class="mt-1 text-sm text-gray-500">聚合模型、价格与安装管理</p>
      </div>
      <div v-if="overview" class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="item in metrics" :key="item.label" class="card p-4">
          <div class="text-sm text-gray-500">{{ item.label }}</div>
          <div class="mt-1 text-xl font-semibold">{{ item.value }}</div>
        </div>
      </div>
      <div class="card min-w-0 space-y-5 p-4 sm:p-6">
        <div role="tablist" class="flex flex-wrap gap-2 border-b border-gray-200 pb-3 dark:border-gray-700">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            role="tab"
            :aria-selected="active === tab.id"
            class="btn"
            :class="active === tab.id ? 'btn-primary' : 'btn-secondary'"
            @click="active = tab.id"
          >{{ tab.label }}</button>
        </div>
        <p v-if="active === 'audio' && overview?.features?.audio === false" class="text-sm text-amber-600">音频调用未启用，可先配置模型与价格。</p>
        <p v-if="active === 'video' && overview?.features?.video === false" class="text-sm text-amber-600">视频调用未启用，可先配置模型与价格。</p>
        <LlmModelsPanel v-if="active === 'llm'" />
        <ImageModelsPanel v-else-if="active === 'images'" />
        <AudioModelsPanel v-else-if="active === 'audio'" />
        <VideoModelsPanel v-else-if="active === 'video'" />
        <StoragePanel v-else-if="active === 'storage'" />
      </div>
      <div class="card p-4 sm:p-6"><InstallList /></div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import LlmModelsPanel from '@/components/admin/donebot/LlmModelsPanel.vue'
import ImageModelsPanel from '@/components/admin/donebot/ImageModelsPanel.vue'
import AudioModelsPanel from '@/components/admin/donebot/AudioModelsPanel.vue'
import VideoModelsPanel from '@/components/admin/donebot/VideoModelsPanel.vue'
import StoragePanel from '@/components/admin/donebot/StoragePanel.vue'
import InstallList from '@/components/admin/donebot/InstallList.vue'
import donebot, { type Overview, type Modality } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const route = useRoute()
const active = ref<Modality | 'storage'>(route.query.tab === 'storage' ? 'storage' : 'llm')
const overview = ref<Overview | null>(null)
const tabs = [{ id: 'llm' as const, label: 'LLM' }, { id: 'images' as const, label: '图片' }, { id: 'audio' as const, label: '音频' }, { id: 'video' as const, label: '视频' }, { id: 'storage' as const, label: '存储' }]
const metrics = computed(() => [
  { label: '已绑定用户', value: overview.value?.user_count },
  { label: '启用模型', value: overview.value?.model_count },
  { label: '今日请求', value: overview.value?.today_usage?.total_requests ?? 0 },
  { label: '今日费用（USD）', value: (overview.value?.today_usage?.total_actual_cost ?? 0).toFixed(4) }
])

onMounted(async () => {
  try {
    overview.value = await donebot.overview()
  } catch (error) {
    app.showError(error instanceof Error ? error.message : '加载失败')
  }
})
</script>
