<template>
  <form class="space-y-4 rounded-xl border border-gray-200 bg-gray-50 p-5 dark:border-gray-700 dark:bg-gray-800/40" data-testid="release-storage" @submit.prevent="save">
    <div><h3 class="font-semibold">客户端安装包 · 独立存储</h3><p class="mt-1 text-sm text-gray-500">仅用于 EXE、DMG 和 ZIP 发行包。与素材存储分别配置，文件长期保留；切换配置后，已有版本仍从原位置下载。</p></div>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="!draft" class="text-sm text-gray-500">{{ busy ? '加载中…' : '未能加载配置' }}</p>
    <template v-if="draft">
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block text-sm"><span class="mb-1 block">存储方式</span><select v-model="draft.driver" class="input" :disabled="busy" data-testid="release-storage-driver"><option value="local">服务器本地</option><option value="s3">S3 / OSS / R2</option></select></label>
        <label v-if="draft.driver === 'local'" class="block text-sm"><span class="mb-1 block">安装包专用目录</span><input v-model.trim="draft.local_path" class="input" placeholder="data/client-packages" :disabled="busy" data-testid="release-storage-path"></label>
      </div>
      <div v-if="draft.driver === 's3'" class="grid gap-4 sm:grid-cols-2">
        <label class="block text-sm"><span class="mb-1 block">Endpoint</span><input v-model.trim="draft.s3.endpoint" type="url" class="input" placeholder="https://账户.r2.cloudflarestorage.com" :disabled="busy"></label>
        <label class="block text-sm"><span class="mb-1 block">Region</span><input v-model.trim="draft.s3.region" class="input" placeholder="auto" :disabled="busy"></label>
        <label class="block text-sm"><span class="mb-1 block">Bucket</span><input v-model.trim="draft.s3.bucket" class="input" :disabled="busy" data-testid="release-storage-bucket"></label>
        <label class="block text-sm"><span class="mb-1 block">对象前缀</span><input v-model.trim="draft.s3.prefix" class="input" placeholder="downloads/" :disabled="busy"></label>
        <label class="block text-sm"><span class="mb-1 block">Access Key ID</span><input v-model.trim="draft.s3.access_key_id" class="input" autocomplete="off" :disabled="busy"></label>
        <label class="block text-sm"><span class="mb-1 block">Secret Access Key</span><input v-model="draft.s3.secret_access_key" type="password" class="input" autocomplete="new-password" :placeholder="draft.secret_configured ? '已配置，留空保留原值' : '填写独立存储密钥'" :disabled="busy" data-testid="release-storage-secret"></label>
        <label class="block text-sm sm:col-span-2"><span class="mb-1 block">存储公网地址（可选）</span><input v-model.trim="draft.s3.public_base_url" type="url" class="input" placeholder="https://downloads.example.com" :disabled="busy"></label>
        <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="draft.s3.force_path_style" type="checkbox" :disabled="busy">使用 Path Style（MinIO 等）</label>
      </div>
      <div class="flex flex-wrap items-center gap-3"><button class="btn btn-primary" :disabled="busy" data-testid="save-release-storage">{{ busy ? '处理中…' : '保存安装包存储设置' }}</button><span v-if="saved" role="status" class="text-sm text-gray-500">独立配置已保存</span></div>
    </template>
    <button v-if="!draft && !busy" type="button" class="btn btn-secondary" @click="load">重新加载</button>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot, { type ClientReleaseStorageSettings } from '@/api/admin/donebot'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
const stepUp = useStepUp()
const draft = ref<ClientReleaseStorageSettings | null>(null)
const busy = ref(false)
const saved = ref(false)
const error = ref('')
async function load() {
  busy.value = true; error.value = ''
  try { draft.value = await donebot.clientReleaseStorage() }
  catch { error.value = '加载安装包存储失败，请重试' }
  finally { busy.value = false }
}
async function save() {
  if (!draft.value || busy.value) return
  busy.value = true; error.value = ''; saved.value = false
  try {
    const input = { ...draft.value, s3: { ...draft.value.s3 } }
    draft.value.s3.secret_access_key = ''
    draft.value = await stepUp.run(() => donebot.saveClientReleaseStorage(input))
    saved.value = true
  } catch (cause) { if (!isStepUpCancelled(cause)) error.value = '保存安装包存储失败，请检查配置后重试' }
  finally { busy.value = false }
}
onMounted(load)
</script>
