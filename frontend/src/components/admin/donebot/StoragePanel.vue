<template>
  <form class="min-w-0 space-y-5" @submit.prevent="save">
    <div class="flex flex-wrap items-center gap-3">
      <button type="button" class="btn btn-primary" data-testid="save-storage" :disabled="busy || !drafts || !!validationError" @click="save">保存存储设置</button>
      <button type="button" class="btn btn-secondary" data-testid="reload-storage" :disabled="busy" @click="load">刷新用量</button>
      <span class="text-sm text-gray-500">保存后生效。容量超过清理阈值时，按时间从早到晚删除，直到占用降至阈值以内。</span>
    </div>
    <p v-if="error || validationError" role="alert" class="text-sm text-red-600">{{ error || validationError }}</p>
    <p v-if="busy && !drafts" class="text-sm text-gray-500">加载中…</p>
    <div v-if="drafts" class="space-y-6">
      <article v-for="kind in kinds" :key="kind.id" :data-testid="`storage-${kind.id}`" class="min-w-0 space-y-5 rounded-xl border border-gray-200 p-4 dark:border-gray-700 sm:p-5">
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div><h3 class="font-semibold">{{ kind.title }}</h3><p class="mt-1 text-sm text-gray-500">{{ kind.description }}</p></div>
          <label class="flex items-center gap-2 text-sm"><input v-model="drafts[kind.id].policy.enabled" type="checkbox" :disabled="busy" :aria-label="`启用${kind.title}`">启用</label>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-gray-800/50">
          <div class="flex flex-wrap justify-between gap-2 text-sm"><span>{{ formatBytes(drafts[kind.id].usage) }} / {{ formatBytes(Number(drafts[kind.id].capacityGiB) * GiB) }}</span><span>{{ drafts[kind.id].fileCount }} 个文件 · {{ usagePercent(drafts[kind.id]) }}%</span></div>
          <div class="mt-2 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700" role="progressbar" :aria-label="`${kind.title}容量使用率`" :aria-valuenow="Number(usagePercent(drafts[kind.id]))" aria-valuemin="0" aria-valuemax="100"><div class="h-full rounded-full bg-primary-500" :style="{ width: `${Math.min(100, Number(usagePercent(drafts[kind.id])))}%` }" /></div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm"><span class="mb-1 block">存储方式</span><select v-model="drafts[kind.id].policy.driver" class="input" :data-testid="`${kind.id}-driver`" :disabled="busy"><option value="local">本地存储</option><option value="s3">S3 兼容存储</option></select></label>
          <label class="block text-sm"><span class="mb-1 block">公网访问地址</span><input v-model.trim="drafts[kind.id].policy.public_base_url" type="url" class="input" placeholder="https://media.example.com" :data-testid="`${kind.id}-public-url`" :disabled="busy"><span class="mt-1 block text-xs text-gray-500">{{ kind.id === 'upload' ? '上传存储可留空，使用上传请求的网关地址。' : '本地生成产物必须填写可公开访问的网关地址。' }}</span></label>
          <label v-if="drafts[kind.id].policy.driver === 'local'" class="block text-sm sm:col-span-2"><span class="mb-1 block">本地目录</span><input v-model.trim="drafts[kind.id].policy.local_path" class="input" :data-testid="`${kind.id}-local-path`" :disabled="busy" placeholder="data/media"></label>
        </div>
        <div v-if="drafts[kind.id].policy.driver === 's3'" class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm"><span class="mb-1 block">S3 Endpoint</span><input v-model.trim="drafts[kind.id].policy.s3.endpoint" type="url" class="input" placeholder="留空使用 AWS S3" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Region</span><input v-model.trim="drafts[kind.id].policy.s3.region" class="input" placeholder="auto" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Bucket</span><input v-model.trim="drafts[kind.id].policy.s3.bucket" class="input" :data-testid="`${kind.id}-bucket`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">对象前缀</span><input v-model.trim="drafts[kind.id].policy.s3.prefix" class="input" placeholder="media/" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Access Key ID</span><input v-model.trim="drafts[kind.id].policy.s3.access_key_id" class="input" autocomplete="off" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Secret Access Key</span><input v-model="drafts[kind.id].policy.s3.secret_access_key" type="password" class="input" autocomplete="new-password" :data-testid="`${kind.id}-secret`" :placeholder="drafts[kind.id].secretConfigured ? '已配置，留空保留原值' : '填写 S3 Secret Access Key'" :disabled="busy"></label>
          <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="drafts[kind.id].policy.s3.force_path_style" type="checkbox" :disabled="busy">使用 Path Style（MinIO 等）</label>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <label class="block text-sm"><span class="mb-1 block">最大总容量（GiB）</span><input v-model.number="drafts[kind.id].capacityGiB" type="number" min="0.000001" step="any" class="input" :data-testid="`${kind.id}-capacity`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">单文件上限（MiB）</span><input v-model.number="drafts[kind.id].maxFileMiB" type="number" min="0.000001" step="any" class="input" :data-testid="`${kind.id}-max-file`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">最长存储时长（小时）</span><input v-model.number="drafts[kind.id].policy.ttl_hours" type="number" min="1" max="8760" step="1" class="input" :data-testid="`${kind.id}-ttl`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">清理阈值（%）</span><input v-model.number="drafts[kind.id].policy.cleanup_threshold_percent" type="number" min="1" max="100" step="1" class="input" :data-testid="`${kind.id}-threshold`" :disabled="busy"></label>
        </div>
      </article>
    </div>
  </form>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import donebot, { type StoragePolicy, type StoragePolicyInput, type StorageSettings } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

type StorageKind = 'upload' | 'generated'
interface PolicyDraft { policy: StoragePolicyInput; capacityGiB: number; maxFileMiB: number; usage: number; fileCount: number; secretConfigured: boolean }
const GiB = 1024 ** 3
const MiB = 1024 ** 2
const kinds: { id: StorageKind; title: string; description: string }[] = [
  { id: 'upload', title: '上传存储', description: '保存用户上传的素材，使用独立容量与保留策略。' },
  { id: 'generated', title: '生成产物存储', description: '视频、音乐和异步图片共用此存储策略。' }
]
const app = useAppStore()
const stepUp = useStepUp()
const drafts = ref<Record<StorageKind, PolicyDraft> | null>(null)
const busy = ref(false)
const error = ref('')

function toDraft(value: StoragePolicy): PolicyDraft {
  return {
    policy: {
      enabled: value.enabled, driver: value.driver, local_path: value.local_path, public_base_url: value.public_base_url,
      max_storage_bytes: value.max_storage_bytes, ttl_hours: value.ttl_hours, cleanup_threshold_percent: value.cleanup_threshold_percent, max_file_bytes: value.max_file_bytes,
      s3: { ...value.s3, secret_access_key: '' }
    },
    capacityGiB: value.max_storage_bytes / GiB, maxFileMiB: value.max_file_bytes / MiB,
    usage: value.usage_bytes ?? 0, fileCount: value.file_count ?? 0, secretConfigured: value.secret_configured ?? false
  }
}
function apply(value: StorageSettings) { drafts.value = { upload: toDraft(value.upload), generated: toDraft(value.generated) } }
function httpURL(value: string) { try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false } }
const validationError = computed(() => {
  if (!drafts.value) return ''
  for (const { id, title } of kinds) {
    const draft = drafts.value[id], policy = draft.policy
    if (![[draft.capacityGiB, GiB], [draft.maxFileMiB, MiB]].every(([value, unit]) => Number.isFinite(value) && value > 0 && Math.round(value * unit) > 0 && Number.isSafeInteger(Math.round(value * unit)))) return `${title}的容量和单文件上限必须大于 0。`
    if (!Number.isInteger(policy.ttl_hours) || policy.ttl_hours < 1 || policy.ttl_hours > 8760) return `${title}的存储时长必须为 1–8760 小时。`
    if (!Number.isInteger(policy.cleanup_threshold_percent) || policy.cleanup_threshold_percent < 1 || policy.cleanup_threshold_percent > 100) return `${title}的清理阈值必须为 1–100%。`
    if (policy.public_base_url && !httpURL(policy.public_base_url)) return `${title}的公网地址必须是完整的 HTTP(S) URL。`
    if (policy.driver === 'local') {
      if (!policy.local_path.trim()) return `${title}需要填写本地目录。`
      if (id === 'generated' && policy.enabled && !httpURL(policy.public_base_url)) return '本地生成产物存储需要填写可公开访问的公网 HTTP(S) 地址。'
    } else {
      if (policy.s3.endpoint && !httpURL(policy.s3.endpoint)) return `${title}的 S3 Endpoint 必须是完整的 HTTP(S) URL。`
      if (policy.enabled && (!policy.s3.bucket.trim() || !policy.s3.access_key_id.trim() || (!draft.secretConfigured && !policy.s3.secret_access_key))) return `${title}需要填写 S3 Bucket、Access Key ID 和 Secret Access Key。`
      if (policy.enabled && policy.ttl_hours > 168 && !httpURL(policy.public_base_url)) return `${title}的 S3 保留时间超过 168 小时，需要填写公网访问地址。`
    }
  }
  return ''
})
function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 B'
  if (bytes >= GiB) return `${Number((bytes / GiB).toFixed(2))} GiB`
  if (bytes >= MiB) return `${Number((bytes / MiB).toFixed(2))} MiB`
  if (bytes >= 1024) return `${Number((bytes / 1024).toFixed(2))} KiB`
  return `${bytes} B`
}
function usagePercent(draft: PolicyDraft) { return draft.capacityGiB > 0 ? Math.max(0, draft.usage / (draft.capacityGiB * GiB) * 100).toFixed(1) : '0.0' }
async function load() {
  busy.value = true; error.value = ''
  try {
    const value = await donebot.storage()
    if (!drafts.value) apply(value)
    else for (const { id } of kinds) {
      drafts.value[id].usage = value[id].usage_bytes ?? 0
      drafts.value[id].fileCount = value[id].file_count ?? 0
      drafts.value[id].secretConfigured = value[id].secret_configured ?? false
    }
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '加载存储设置失败' } finally { busy.value = false }
}
function toInput(draft: PolicyDraft): StoragePolicyInput {
  return { ...draft.policy, max_storage_bytes: Math.round(draft.capacityGiB * GiB), max_file_bytes: Math.round(draft.maxFileMiB * MiB), s3: { ...draft.policy.s3 } }
}
async function save() {
  if (!drafts.value || validationError.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const input = { upload: toInput(drafts.value.upload), generated: toInput(drafts.value.generated) }
    apply(await stepUp.run(() => donebot.saveStorage(input)))
    app.showSuccess('存储设置已保存')
  } catch (cause) { if (!isStepUpCancelled(cause)) error.value = cause instanceof Error ? cause.message : '保存存储设置失败' } finally { busy.value = false }
}
onMounted(load)
</script>
