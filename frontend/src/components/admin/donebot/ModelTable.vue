<template>
 <div class="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-700">
  <table class="w-full min-w-[640px] text-left text-sm">
   <thead class="bg-gray-50 text-gray-600 dark:bg-dark-800 dark:text-gray-300"><tr><th class="p-3">模型 / 平台</th><th class="p-3">启用</th><th v-if="kind==='llm'" class="p-3">渠道价 输入 / 输出<br><span class="font-normal">USD / 百万 token</span></th><th class="p-3">有效价（USD{{ kind==='llm' ? ' / 百万 token' : ' / 张' }}）</th></tr></thead>
   <tbody class="divide-y divide-gray-100 dark:divide-gray-700">
    <tr v-for="row in models" :key="row.model"><td class="p-3"><div class="font-medium">{{ row.model }}</div><div class="text-xs text-gray-500">{{ row.platform }} <span v-if="!row.available" class="text-amber-600">· 当前渠道不可用</span></div></td><td class="p-3"><input v-model="row.enabled" type="checkbox" class="h-4 w-4 rounded" :aria-label="`启用 ${row.model}`" :disabled="disabled || (!row.available && !row.enabled)"></td><td v-if="kind==='llm'" class="p-3 tabular-nums">{{ price(row.channel_pricing?.input_price, 1e6) }} / {{ price(row.channel_pricing?.output_price, 1e6) }}</td><td class="p-3 tabular-nums"><template v-if="kind==='llm'">{{ price(row.effective_input_price, 1e6) }} / {{ price(row.effective_output_price, 1e6) }}</template><template v-else><div v-for="size in ['1K','2K','4K']" :key="size">{{ size }}: {{ price(row.effective_image_prices?.[size]) }}</div></template></td></tr>
    <tr v-if="models.length===0"><td colspan="4" class="p-8 text-center text-gray-500">暂无模型。先为 DoneBot 分组配置可用账号、渠道定价和图片路由，再同步模型。</td></tr>
   </tbody>
  </table>
 </div>
</template>
<script setup lang="ts">
import type { DoneBotModel, Modality } from '@/api/admin/donebot'
defineProps<{ models: DoneBotModel[]; kind: Modality; disabled: boolean }>()
function price(value: number | null | undefined, factor=1) { return value==null ? '—' : (value*factor).toFixed(4) }
</script>
