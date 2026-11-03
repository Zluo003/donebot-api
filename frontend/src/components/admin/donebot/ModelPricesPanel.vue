<template>
  <section class="min-w-0 space-y-5">
    <div class="flex flex-wrap gap-3">
      <button class="btn btn-secondary" data-test="sync-models" :disabled="busy" @click="sync">同步模型</button>
      <button class="btn btn-primary" data-test="save-models" :disabled="busy || !state" @click="saveModels">保存模型开关</button>
      <button class="btn btn-primary" data-test="save-pricing" :disabled="busy || !editableDrafts.length || !validPrices" @click="savePrices">保存模型价格</button>
    </div>
    <p class="text-sm text-gray-500">{{ descriptions[kind] }} 价格为每个模型的基础售价，保存后按此价格计费；单个模型全部留空可恢复渠道配置，媒体未设置档位不可调用；已有高级阶梯定价保留。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="!validPrices" role="alert" class="text-sm text-red-600">价格必须为大于或等于 0 的有效数字。</p>
    <div v-if="state" class="space-y-4">
      <article v-for="(row, index) in state.models" :key="row.model" class="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0"><h3 class="break-all font-medium">{{ row.model }}</h3><p class="text-xs text-gray-500">{{ row.platform }} <span v-if="!row.available" class="text-amber-600">· 当前渠道不可用</span><span v-else-if="row.pricing_configured === false" class="text-amber-600">· 请先保存模型价格</span></p></div>
          <label class="flex items-center gap-2 text-sm"><input v-model="row.enabled" type="checkbox" class="h-4 w-4 rounded" :aria-label="`启用 ${row.model}`" :disabled="busy || (!row.enabled && (!row.available || row.pricing_configured === false))">启用</label>
        </div>
        <template v-if="drafts[index]">
          <div v-if="kind === 'video'" class="overflow-x-auto">
            <table class="w-full min-w-[420px] text-left text-sm">
              <thead><tr><th class="py-2">分辨率</th><th class="px-2 py-2">无参考视频（USD / 秒）</th><th class="px-2 py-2">有参考视频（USD / 秒）</th></tr></thead>
              <tbody>
                <tr v-for="resolution in videoResolutions(drafts[index])" :key="resolution">
                  <td class="py-2 font-medium">{{ resolution === '4k' ? '4K' : resolution }}</td>
                  <td v-for="field in drafts[index].fields.filter(item => item.resolution === resolution)" :key="field.key" class="px-2 py-2">
                    <input v-model="field.value" type="number" min="0" step="any" class="input" placeholder="未配置" :aria-label="`${row.model} ${resolution} ${field.label}价格`" :data-testid="`price-${row.model}-${field.key}`" :disabled="busy || !row.available">
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-if="!drafts[index].fields.length" class="text-sm text-amber-600">该模型尚未提供分辨率规格，请先配置模型映射。</p>
          </div>
          <div v-else class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <label v-for="field in drafts[index].fields" :key="field.key" class="block text-sm">
              <span class="mb-1 block">{{ field.label }}<span class="ml-1 text-xs text-gray-500">{{ priceUnit(row) }}</span></span>
              <input v-model="field.value" type="number" min="0" step="any" class="input" placeholder="未配置" :aria-label="`${row.model} ${field.label}价格`" :data-testid="`price-${row.model}-${field.key}`" :disabled="busy || !row.available">
            </label>
          </div>
        </template>
      </article>
      <p v-if="state.models.length === 0" class="p-6 text-center text-gray-500">暂无模型。先为 DoneBot 分组绑定可用账号，再同步模型。</p>
    </div>
    <p v-else-if="busy" class="text-gray-500">加载中…</p>
    <slot />
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { DoneBotModel, Modality } from '@/api/admin/donebot'
import { useDoneBotModels } from './useDoneBotModels'
import { createModelPriceDraft, draftToSalePricing, validPriceDraft, type ModelPriceDraft } from './modelSalePricing'
const props = defineProps<{ kind: Modality }>()
const { state, busy, error, sync, saveModels, savePricing } = useDoneBotModels(props.kind)
const drafts = ref<ModelPriceDraft[]>([])
const savedDrafts = new Map<string, string>()
watch(state, value => {
  const previous = new Map(drafts.value.map(draft => [draft.model, draft]))
  const nextSaved = new Map<string, string>()
  drafts.value = (value?.models ?? []).map(row => {
    const draft = createModelPriceDraft(row, props.kind)
    const snapshot = JSON.stringify(draft)
    nextSaved.set(row.model, snapshot)
    return savedDrafts.get(row.model) === snapshot ? previous.get(row.model) ?? draft : draft
  })
  savedDrafts.clear()
  for (const [model, snapshot] of nextSaved) savedDrafts.set(model, snapshot)
})
const editableDrafts = computed(() => drafts.value.filter(draft => draft.fields.length > 0 && state.value?.models.find(row => row.model === draft.model)?.available))
const validPrices = computed(() => drafts.value.every(validPriceDraft))
const descriptions: Record<Modality, string> = {
  llm: '输入、输出和缓存按百万 token 配置（USD / 百万 token）。',
  images: '图片按模型与尺寸逐张计费（USD / 张）。Seedream 1.5K 与 1K 使用相同价格。',
  audio: 'Suno 按版本配置每次生成价格，Max 模式为 2 倍；TTS 按百万字符配置价格。',
  video: '视频按实际输出时长计费（USD / 秒），按模型、官方分辨率及是否使用参考视频分别配置。'
}
function priceUnit(row: DoneBotModel) {
  if (props.kind === 'llm') return 'USD / 百万 token'
  if (props.kind === 'images') return 'USD / 张'
  return row.audio_price_unit === 'million_characters' || row.model === 'gpt-4o-mini-tts' || drafts.value.find(draft => draft.model === row.model)?.fields.some(field => field.label === 'tts') ? 'USD / 百万字符' : 'USD / 次生成'
}
function videoResolutions(draft: ModelPriceDraft) { return [...new Set(draft.fields.map(field => field.resolution!))] }
function savePrices() {
  if (!validPrices.value || !state.value) return
  void savePricing({ models: editableDrafts.value.map(draft => ({ model: draft.model, pricing: draftToSalePricing(draft, props.kind) })) })
}
</script>
