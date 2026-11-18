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
          <label class="block text-sm"><span class="mb-1 block">存储方式</span><select v-model="drafts[kind.id].policy.driver" class="input" :data-testid="`${kind.id}-driver`" :disabled="busy"><option value="local">本地存储</option><option value="s3">S3 / R2 兼容存储</option><option value="oss">阿里云 OSS</option></select></label>
          <label v-if="drafts[kind.id].policy.driver === 'local'" class="block text-sm"><span class="mb-1 block">本地网关地址</span><input v-model.trim="drafts[kind.id].policy.local_public_base_url" type="url" class="input" placeholder="https://donebot.cn" :data-testid="`${kind.id}-public-url`" :disabled="busy"><span class="mt-1 block text-xs text-gray-500">{{ kind.id === 'upload' ? '可留空，使用上传请求的网关地址。' : '填写可公开访问的网关地址。' }}与 S3 / R2 的公网地址分别保存。</span></label>
          <label v-else-if="drafts[kind.id].policy.driver === 's3'" class="block text-sm"><span class="mb-1 block">S3 / R2 公网地址</span><input v-model.trim="drafts[kind.id].policy.s3.public_base_url" type="url" class="input" placeholder="https://media.example.com" :data-testid="`${kind.id}-public-url`" :disabled="busy"><span class="mt-1 block text-xs text-gray-500">填写存储桶的公网域名，留空使用预签名链接。与本地网关地址分别保存。</span></label>
          <label v-if="drafts[kind.id].policy.driver === 'local'" class="block text-sm sm:col-span-2"><span class="mb-1 block">本地根目录</span><input v-model.trim="drafts[kind.id].policy.local_path" class="input" :data-testid="`${kind.id}-local-path`" :disabled="busy" placeholder="data/media"><span class="mt-1 block text-xs text-gray-500">文件保存在此根目录的 {{ kind.id }} 子目录中；此目录与 S3 / R2 对象前缀分别保存。</span></label>
        </div>
        <div v-if="drafts[kind.id].policy.driver === 's3'" class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm"><span class="mb-1 block">S3 Endpoint</span><input v-model.trim="drafts[kind.id].policy.s3.endpoint" type="url" class="input" placeholder="留空使用 AWS S3" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Region</span><input v-model.trim="drafts[kind.id].policy.s3.region" class="input" placeholder="auto" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Bucket</span><input v-model.trim="drafts[kind.id].policy.s3.bucket" class="input" :data-testid="`${kind.id}-bucket`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">对象前缀</span><input v-model.trim="drafts[kind.id].policy.s3.prefix" class="input" placeholder="media/" :data-testid="`${kind.id}-s3-prefix`" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Access Key ID</span><input v-model.trim="drafts[kind.id].policy.s3.access_key_id" class="input" autocomplete="off" :disabled="busy"></label>
          <label class="block text-sm"><span class="mb-1 block">Secret Access Key</span><input v-model="drafts[kind.id].policy.s3.secret_access_key" type="password" class="input" autocomplete="new-password" :data-testid="`${kind.id}-secret`" :placeholder="drafts[kind.id].policy.s3.secret_configured ? '已配置，留空保留原值' : '填写 S3 Secret Access Key'" :disabled="busy"></label>
          <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="drafts[kind.id].policy.s3.force_path_style" type="checkbox" :disabled="busy">使用 Path Style（MinIO 等）</label>
        </div>
        <OSSStorageFields v-if="drafts[kind.id].policy.driver === 'oss'" v-model="drafts[kind.id].policy.oss" :scope="kind.id" :disabled="busy" />
        <div v-if="drafts[kind.id].policy.driver !== 'local'" class="space-y-2">
          <button type="button" class="btn btn-secondary" :data-testid="`test-storage-${kind.id}`" :disabled="busy" @click="testConnection(kind.id)">{{ testing === kind.id ? '测试中…' : '测试连通性' }}</button>
          <p class="text-xs text-gray-500">使用当前表单配置测试写入、读取、下载链接和删除，不保存配置；仅创建并清理唯一的临时测试文件。修改 Endpoint 或 Access Key ID 后测试需重新填写密钥。</p>
          <p v-if="testResults[kind.id]" :role="testResults[kind.id]!.ok ? 'status' : 'alert'" :class="testResults[kind.id]!.ok ? 'text-sm text-green-600' : 'text-sm text-red-600'" :data-testid="`storage-test-result-${kind.id}`">{{ testResults[kind.id]!.message }}</p>
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
import { computed, onMounted, ref, watch } from 'vue'
import donebot, { type StoragePolicy, type StoragePolicyInput, type StorageSettings, type ObjectStorageSettings, ossStorageDraft } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import OSSStorageFields from './OSSStorageFields.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

type StorageKind = 'upload' | 'generated'
interface PolicyDraft { policy: StoragePolicyInput & { oss: ObjectStorageSettings }; capacityGiB: number; maxFileMiB: number; usage: number; fileCount: number }
const GiB = 1024 ** 3
const MiB = 1024 ** 2
const kinds: { id: StorageKind; title: string; description: string }[] = [
  { id: 'upload', title: '上传存储', description: '保存用户上传的图片、文档、音频等素材。' },
  { id: 'generated', title: '生成产物存储', description: '视频、音乐和异步图片共用此存储策略。' }
]
const app = useAppStore()
const stepUp = useStepUp()
const drafts = ref<Record<StorageKind, PolicyDraft> | null>(null)
const busy = ref(false)
const error = ref('')
const testing = ref<StorageKind | null>(null)
const testResults = ref<Partial<Record<StorageKind, { ok: boolean; message: string }>>>({})
for (const { id } of kinds) watch(() => drafts.value && toInput(drafts.value[id]), () => { delete testResults.value[id] }, { deep: true })

function toDraft(value: StoragePolicy): PolicyDraft {
  return {
    policy: {
      enabled: value.enabled, driver: value.driver, local_path: value.local_path, public_base_url: value.public_base_url,
      local_public_base_url: value.local_public_base_url ?? (value.driver === 'local' ? value.public_base_url : ''),
      max_storage_bytes: value.max_storage_bytes, ttl_hours: value.ttl_hours, cleanup_threshold_percent: value.cleanup_threshold_percent, max_file_bytes: value.max_file_bytes,
      oss: ossStorageDraft(value.oss),
      s3: { ...value.s3, secret_configured: value.s3.secret_configured ?? value.secret_configured, public_base_url: value.s3.public_base_url ?? (value.driver === 's3' ? value.public_base_url : ''), secret_access_key: '' }
    },
    capacityGiB: value.max_storage_bytes / GiB, maxFileMiB: value.max_file_bytes / MiB,
    usage: value.usage_bytes ?? 0, fileCount: value.file_count ?? 0
  }
}
function apply(value: StorageSettings) { drafts.value = { upload: toDraft(value.upload), generated: toDraft(value.generated) } }
function httpURL(value: string) { try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false } }
function publicURL(policy: StoragePolicyInput) { return (policy.driver === 'local' ? policy.local_public_base_url : policy.driver === 'oss' ? policy.oss?.public_base_url : policy.s3.public_base_url) ?? '' }
const validationError = computed(() => {
  if (!drafts.value) return ''
  for (const { id, title } of kinds) {
    const draft = drafts.value[id], policy = draft.policy
    if (![[draft.capacityGiB, GiB], [draft.maxFileMiB, MiB]].every(([value, unit]) => Number.isFinite(value) && value > 0 && Math.round(value * unit) > 0 && Number.isSafeInteger(Math.round(value * unit)))) return `${title}的容量和单文件上限必须大于 0。`
    if (!Number.isInteger(policy.ttl_hours) || policy.ttl_hours < 1 || policy.ttl_hours > 8760) return `${title}的存储时长必须为 1–8760 小时。`
    if (!Number.isInteger(policy.cleanup_threshold_percent) || policy.cleanup_threshold_percent < 1 || policy.cleanup_threshold_percent > 100) return `${title}的清理阈值必须为 1–100%。`
    const activePublicURL = publicURL(policy)
    if (activePublicURL && !httpURL(activePublicURL)) return `${title}的公网地址必须是完整的 HTTP(S) URL。`
    if (policy.driver === 'local') {
      if (!policy.local_path.trim()) return `${title}需要填写本地目录。`
      if (id === 'generated' && policy.enabled && !httpURL(activePublicURL)) return '本地生成产物存储需要填写可公开访问的公网 HTTP(S) 地址。'
    } else {
      const remote = policy.driver === 'oss' ? policy.oss : policy.s3
      if (remote.endpoint && !httpURL(remote.endpoint)) return `${title}的 S3 Endpoint 必须是完整的 HTTP(S) URL。`
      if (policy.enabled && (!remote.bucket.trim() || !remote.access_key_id.trim() || (!remote.secret_configured && !remote.secret_access_key))) return `${title}需要填写 S3 Bucket、Access Key ID 和 Secret Access Key。`
      if (policy.driver === 'oss' && policy.enabled && (!remote.region.trim() || remote.region === 'auto' || !remote.download_endpoint || !httpURL(remote.download_endpoint))) return `${title}需要填写 OSS 实际地域和普通下载 Endpoint。`
      if (policy.enabled && policy.ttl_hours > 168 && !httpURL(activePublicURL)) return policy.driver === 'oss' ? `${title}的 OSS 私有桶存储时长不能超过 168 小时。` : `${title}的 S3 保留时间超过 168 小时，需要填写公网访问地址。`
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
      drafts.value[id].policy.s3.secret_configured = value[id].s3.secret_configured ?? value[id].secret_configured ?? false
      drafts.value[id].policy.oss.secret_configured = value[id].oss?.secret_configured ?? false
    }
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '加载存储设置失败' } finally { busy.value = false }
}
function toInput(draft: PolicyDraft): StoragePolicyInput {
  return { ...draft.policy, public_base_url: publicURL(draft.policy), max_storage_bytes: Math.round(draft.capacityGiB * GiB), max_file_bytes: Math.round(draft.maxFileMiB * MiB), s3: { ...draft.policy.s3 }, oss: { ...draft.policy.oss } }
}
async function testConnection(kind: StorageKind) {
  if (!drafts.value || busy.value) return
  const input = toInput(drafts.value[kind])
  busy.value = true; testing.value = kind; delete testResults.value[kind]
  try { testResults.value[kind] = await stepUp.run(() => donebot.testStorage(kind, input)) }
  catch (cause) { if (!isStepUpCancelled(cause)) testResults.value[kind] = { ok: false, message: '测试请求失败，请检查网络或管理权限后重试。' } }
  finally { busy.value = false; testing.value = null }
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
