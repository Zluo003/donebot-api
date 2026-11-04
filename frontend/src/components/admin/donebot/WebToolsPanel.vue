<template>
  <form class="min-w-0 space-y-5" @submit.prevent="save">
    <div>
      <h3 class="font-semibold">DeepSeek Search / Fetch</h3>
      <p class="mt-2 text-sm leading-relaxed text-gray-500">启用并配置 Key 后，联网搜索优先使用 DeepSeek Search；不可用时回退到模型原生搜索。读取网页 URL 使用 Fetch。</p>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="busy && !loaded" class="text-sm text-gray-500">加载中…</p>
    <div v-if="loaded" class="max-w-xl space-y-4">
      <label class="flex items-center gap-2 text-sm"><input v-model="enabled" type="checkbox" :disabled="busy" aria-label="启用 DeepSeek Search / Fetch">启用 DeepSeek Search / Fetch</label>
      <p class="text-sm text-gray-500" role="status">{{ keyConfigured ? 'API Key 已配置' : 'API Key 未配置' }}</p>
      <label class="block text-sm">
        <span class="mb-1 block">DeepSeek API Key</span>
        <input v-model="apiKey" type="password" class="input" autocomplete="new-password" aria-label="DeepSeek API Key" :disabled="busy || clearApiKey" :placeholder="keyConfigured ? '已配置，留空保留原值' : '填写 DeepSeek API Key'">
      </label>
      <label class="flex items-center gap-2 text-sm"><input v-model="clearApiKey" type="checkbox" :disabled="busy" aria-label="清除已保存的 API Key" @change="apiKey = ''">清除已保存的 API Key</label>
      <p class="text-xs text-gray-500">Key 仅保存在网关，不返回客户端。留空保存会保留已有 Key；勾选清除后保存会删除 Key。</p>
    </div>
    <div class="flex flex-wrap gap-3">
      <button type="submit" class="btn btn-primary" data-testid="save-web-tools" :disabled="busy || !loaded">保存联网工具设置</button>
      <button type="button" class="btn btn-secondary" data-testid="reload-web-tools" :disabled="busy" @click="load">重新加载</button>
    </div>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot, { type WebToolsSettings, type WebToolsSettingsInput } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const app = useAppStore()
const stepUp = useStepUp()
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const enabled = ref(true)
const keyConfigured = ref(false)
const apiKey = ref('')
const clearApiKey = ref(false)

function apply(settings: WebToolsSettings) {
  enabled.value = settings.enabled
  keyConfigured.value = settings.key_configured
  apiKey.value = ''
  clearApiKey.value = false
  loaded.value = true
}

async function load() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  apiKey.value = ''
  clearApiKey.value = false
  try { apply(await donebot.webTools()) } catch { error.value = '加载联网工具设置失败，请重试。' } finally { busy.value = false }
}

async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true
  error.value = ''
  const input: WebToolsSettingsInput = { enabled: enabled.value }
  if (clearApiKey.value) input.clear_api_key = true
  else if (apiKey.value.trim()) input.api_key = apiKey.value.trim()
  apiKey.value = ''
  try {
    apply(await stepUp.run(() => donebot.saveWebTools(input)))
    app.showSuccess('联网工具设置已保存')
  } catch (cause) {
    if (!isStepUpCancelled(cause)) error.value = '保存联网工具设置失败，请重试；如需更换 Key，请重新填写。'
  } finally { delete input.api_key; busy.value = false }
}

onMounted(load)
</script>
