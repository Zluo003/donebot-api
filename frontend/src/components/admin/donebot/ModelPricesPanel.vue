<template>
  <section class="min-w-0 space-y-5">
    <div class="flex flex-wrap gap-3">
      <button class="btn btn-secondary" data-test="sync-models" :disabled="busy" @click="sync">同步模型</button>
      <button class="btn btn-primary" data-test="save-configuration" :disabled="busy || !state?.models.length || !validPrices" @click="save">保存</button>
    </div>
    <p class="text-sm text-gray-500">{{ descriptions[kind] }} <template v-if="kind === 'llm'">倍率为 0 表示免费；输入、输出和缓存单价在渠道中配置。</template><template v-else>价格为每个模型的基础售价；单个模型全部留空可恢复渠道配置，未设置档位不可调用；已有高级阶梯定价保留。</template> 开关与定价一起保存。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="!validPrices" role="alert" class="text-sm text-red-600">{{ kind === 'llm' ? '倍率' : '价格' }}必须为大于或等于 0 的有效数字。</p>
    <div v-if="state" class="space-y-4">
      <article v-for="(row, index) in state.models" :key="row.model" class="rounded-xl border border-gray-200 p-4 dark:border-gray-700">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0"><h3 class="break-all font-medium">{{ row.model }}</h3><p class="text-xs text-gray-500">{{ row.platform }} <span v-if="!row.available" class="text-amber-600">· 当前渠道不可用</span><span v-else-if="row.pricing_configured === false" class="text-amber-600">· {{ kind === 'llm' ? '请先配置渠道价格' : '请先配置模型价格' }}</span></p></div>
          <label class="flex items-center gap-2 text-sm"><input v-model="row.enabled" type="checkbox" class="h-4 w-4 rounded" :aria-label="`启用 ${row.model}`" :disabled="busy || (!row.enabled && !canEnable(row, drafts[index]))">启用</label>
        </div>
        <template v-if="drafts[index]">
          <div v-if="kind === 'video'" class="overflow-x-auto">
            <table class="w-full min-w-[420px] text-left text-sm">
              <thead><tr><th class="py-2">{{ t('admin.donebot.pricing.resolution') }}</th><th class="px-2 py-2">{{ t('admin.donebot.pricing.videoWithoutReference') }}</th><th class="px-2 py-2">{{ t('admin.donebot.pricing.videoWithReference') }}</th></tr></thead>
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
              <input v-model="field.value" type="number" min="0" step="any" class="input" placeholder="未配置" :aria-label="`${row.model} ${field.label}${kind === 'llm' ? '' : '价格'}`" :data-testid="`price-${row.model}-${field.key}`" :disabled="busy || !row.available">
            </label>
          </div>
        </template>
      </article>
      <p v-if="state.models.length === 0" class="p-6 text-center text-gray-500">{{ emptyMessages[kind] }}</p>
    </div>
    <p v-else-if="busy" class="text-gray-500">加载中…</p>
    <slot />
  </section>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DoneBotModel, Modality } from '@/api/admin/donebot'
import { useDoneBotModels } from './useDoneBotModels'
import { createModelPriceDraft, draftToSalePricing, validPriceDraft, type ModelPriceDraft } from './modelSalePricing'
const props = defineProps<{ kind: Modality }>()
const { t } = useI18n()
const { state, busy, error, sync, saveConfiguration } = useDoneBotModels(props.kind)
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
const validPrices = computed(() => drafts.value.every(validPriceDraft))
const descriptions = computed<Record<Modality, string>>(() => ({
  llm: 'LLM 按渠道价格 × 每模型倍率计费。',
  images: '图片按模型与尺寸逐张计费（USD / 张）。Seedream 1.5K 与 1K 使用相同价格。',
  audio: 'Suno 按版本配置每次生成价格，Max 模式为 2 倍；TTS 按百万字符配置价格。',
  video: t('admin.donebot.pricing.videoDescription')
}))
const emptyMessages: Record<Modality, string> = {
  llm: '暂无模型。请为 DoneBot 分组绑定账号，在账号中启用模型，再同步模型。',
  images: '暂无图片模型。请在账号管理添加并启用图片账号，绑定 DoneBot 分组并开启对应图片模型，再同步模型。',
  audio: '暂无音频模型。请在账号管理添加并启用 APIMart 音频账号，绑定 DoneBot 分组并开启 Suno / TTS 模型，再同步模型。',
  video: '暂无视频模型。请在账号管理添加并启用视频账号，绑定 DoneBot 分组并配置允许的模型与分辨率，再同步模型。'
}
function canEnable(row: DoneBotModel, draft?: ModelPriceDraft) {
  if (!row.available) return false
  if (row.pricing_configured !== false) return true
  return props.kind !== 'llm' && !!draft && validPriceDraft(draft) && draft.fields.some(field => String(field.value).trim() !== '')
}
function priceUnit(row: DoneBotModel) {
  if (props.kind === 'llm') return '×'
  if (props.kind === 'images') return 'USD / 张'
  return row.audio_price_unit === 'million_characters' || row.model === 'gpt-4o-mini-tts' || drafts.value.find(draft => draft.model === row.model)?.fields.some(field => field.label === 'tts') ? 'USD / 百万字符' : 'USD / 次生成'
}
function videoResolutions(draft: ModelPriceDraft) { return [...new Set(draft.fields.map(field => field.resolution!))] }
function save() {
  if (!validPrices.value || !state.value) return
  void saveConfiguration({ models: state.value.models.map(row => {
    const draft = drafts.value.find(item => item.model === row.model)
    return { model: row.model, enabled: row.enabled, ...(row.available && draft?.fields.length ? { pricing: draftToSalePricing(draft, props.kind) } : {}) }
  }) })
}
</script>
