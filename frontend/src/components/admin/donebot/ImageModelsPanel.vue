<template>
 <section class="min-w-0 space-y-5">
  <form v-if="state" class="space-y-4" @submit.prevent="save">
   <div class="grid grid-cols-1 gap-4 sm:grid-cols-3"><label v-for="field in fields" :key="field.key" class="block text-sm font-medium">{{ field.label }} 单价（USD / 张）<input v-model="state[field.key]" type="number" min="0" step="0.001" class="input mt-1" placeholder="留空沿用渠道价" :disabled="busy"></label></div>
   <div class="flex flex-wrap items-center gap-4"><label class="flex items-center gap-2 text-sm"><input v-model="state.image_rate_independent" type="checkbox" :disabled="busy">使用独立图片倍率</label><label class="flex items-center gap-2 text-sm"><input v-model="state.image_count_billing" type="checkbox" :disabled="busy">按张计费</label><label class="flex items-center gap-2 text-sm">图片倍率<input v-model.number="state.image_rate_multiplier" type="number" min="0" step="0.05" class="input w-28" :disabled="busy || !state.image_rate_independent"></label><span class="text-sm text-gray-500">分组倍率：{{ state.rate_multiplier }}</span></div>
   <button class="btn btn-primary" :disabled="busy">保存图片价格</button>
  </form>
  <div class="flex flex-wrap gap-3"><button class="btn btn-secondary" :disabled="busy" @click="sync">同步模型</button><button class="btn btn-primary" :disabled="busy || !state" @click="saveModels">保存模型开关</button></div>
  <p class="text-sm text-gray-500">留空使用渠道价；未启用独立倍率时，沿用 LLM 分组倍率。有效价保存后更新。</p>
  <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
  <ImageTaskLookup />
  <ModelTable v-if="state" :models="state.models" kind="images" :disabled="busy" />
 </section>
</template>
<script setup lang="ts">
import type { PricingInput } from '@/api/admin/donebot'
import ModelTable from './ModelTable.vue'
import ImageTaskLookup from './ImageTaskLookup.vue'
import { useDoneBotModels } from './useDoneBotModels'
const { state,busy,error,sync,saveModels,savePricing }=useDoneBotModels('images')
const fields=[{ key:'image_price_1k',label:'1K' },{ key:'image_price_2k',label:'2K' },{ key:'image_price_4k',label:'4K' }] as const
function save(){if(!state.value)return;const data:PricingInput={image_rate_multiplier:Number(state.value.image_rate_multiplier),image_rate_independent:state.value.image_rate_independent,image_count_billing:state.value.image_count_billing,clear_image_prices:[]};for(const field of fields){const raw=state.value[field.key];if(raw==null || String(raw)===''){data.clear_image_prices!.push(field.label)}else data[field.key]=Number(raw)}void savePricing(data)}
</script>
