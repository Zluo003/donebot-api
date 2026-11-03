<template>
 <section class="min-w-0 space-y-5">
  <div class="flex flex-wrap items-end gap-3">
   <label class="w-full sm:w-48"><span class="mb-1 block text-sm font-medium">DoneBot LLM 倍率</span><input v-if="state" v-model.number="state.rate_multiplier" class="input" type="number" min="0.001" step="0.05" :disabled="busy"></label>
   <button class="btn btn-primary" :disabled="busy || !state || state.rate_multiplier<=0" @click="saveRate">保存倍率</button>
   <button class="btn btn-secondary" :disabled="busy" @click="sync">同步模型</button>
   <button class="btn btn-primary" data-test="save-models" :disabled="busy || !state" @click="saveModels">保存模型开关</button>
  </div>
  <p class="text-sm text-gray-500">渠道单价 × 分组倍率；缓存与阶梯定价沿用渠道配置。有效价由服务端计算，保存后更新。</p>
  <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
  <ModelTable v-if="state" :models="state.models" kind="llm" :disabled="busy" />
  <p v-else-if="busy" class="text-gray-500">加载中…</p>
 </section>
</template>
<script setup lang="ts">
import ModelTable from './ModelTable.vue'
import { useDoneBotModels } from './useDoneBotModels'
const { state,busy,error,sync,saveModels,savePricing }=useDoneBotModels('llm')
function saveRate(){if(state.value) void savePricing({ rate_multiplier: state.value.rate_multiplier })}
</script>
