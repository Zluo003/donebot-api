<template>
  <section class="min-w-0 space-y-7" data-testid="client-releases">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h2 class="text-lg font-semibold">客户端版本发布</h2>
        <p class="mt-1 max-w-2xl text-sm text-gray-500">同一版本需上传 Windows 安装器、macOS 安装镜像和在线更新包。</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-secondary" :aria-expanded="showStorage" @click="showStorage = !showStorage">安装包独立存储设置</button>
        <button class="btn btn-secondary" :disabled="busy" @click="load">刷新版本</button>
      </div>
    </div>

    <ClientReleaseStoragePanel v-if="showStorage" />

    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>

    <div class="grid gap-4 md:grid-cols-[220px_1fr]">
      <label class="block text-sm">
        <span class="mb-1 block">版本号</span>
        <input v-model.trim="version" class="input" placeholder="0.1.0" autocomplete="off" data-testid="release-version" aria-describedby="release-version-hint" :disabled="busy">
        <span id="release-version-hint" class="mt-1 block text-xs text-gray-500">请先填写版本号，再上传安装包。</span>
      </label>
      <label class="block text-sm">
        <span class="mb-1 block">更新说明</span>
        <input v-model="notes" class="input" placeholder="本次版本改进" maxlength="12000" data-testid="release-notes" :disabled="busy">
      </label>
    </div>

    <div class="grid gap-7 border-y border-gray-200 py-6 dark:border-gray-700 md:grid-cols-2 xl:grid-cols-3">
      <section class="min-w-0 space-y-3">
        <div>
          <h3 class="font-semibold">Windows x64</h3>
          <p class="mt-1 text-xs text-gray-500">Electron 安装器 · .exe · 最大 500 MB</p>
        </div>
        <input ref="windowsInput" type="file" accept=".exe,application/vnd.microsoft.portable-executable" class="block w-full text-sm text-gray-600 file:mr-3 file:rounded file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-sm dark:text-gray-300 dark:file:bg-gray-800" data-testid="release-windows-file" :disabled="busy" @change="selectFile('windows-x64', $event)">
        <button class="btn btn-secondary" :disabled="busy || !selected['windows-x64'] || !normalizedVersion" data-testid="upload-windows" @click="upload('windows-x64')">上传 Windows 包</button>
        <p v-if="currentDraft?.platforms['windows-x64']" class="text-xs text-gray-500">已保存：{{ currentDraft.platforms['windows-x64']?.filename }} · {{ formatSize(currentDraft.platforms['windows-x64']?.size_bytes ?? 0) }}</p>
      </section>

      <section class="min-w-0 space-y-3">
        <div>
          <h3 class="font-semibold">macOS Apple 芯片 · 新用户安装</h3>
          <p class="mt-1 text-xs text-gray-500">.dmg 安装镜像 · 官网下载 · 最大 500 MB</p>
        </div>
        <input ref="macDmgInput" type="file" accept=".dmg,application/x-apple-diskimage" class="block w-full text-sm text-gray-600 file:mr-3 file:rounded file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-sm dark:text-gray-300 dark:file:bg-gray-800" data-testid="release-mac-dmg-file" :disabled="busy" @change="selectFile('macos-arm64-dmg', $event)">
        <button class="btn btn-secondary" :disabled="busy || !selected['macos-arm64-dmg'] || !normalizedVersion" data-testid="upload-macos-dmg" @click="upload('macos-arm64-dmg')">上传 macOS DMG</button>
        <p v-if="currentDraft?.platforms['macos-arm64-dmg']" class="text-xs text-gray-500">已保存：{{ currentDraft.platforms['macos-arm64-dmg']?.filename }} · {{ formatSize(currentDraft.platforms['macos-arm64-dmg']?.size_bytes ?? 0) }}</p>
      </section>

      <section class="min-w-0 space-y-3">
        <div>
          <h3 class="font-semibold">macOS Apple 芯片 · 客户端升级</h3>
          <p class="mt-1 text-xs text-gray-500">.zip 更新包 · 在线升级使用 · 最大 500 MB</p>
        </div>
        <input ref="macZipInput" type="file" accept=".zip,application/zip" class="block w-full text-sm text-gray-600 file:mr-3 file:rounded file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-sm dark:text-gray-300 dark:file:bg-gray-800" data-testid="release-mac-zip-file" :disabled="busy" @change="selectFile('macos-arm64', $event)">
        <button class="btn btn-secondary" :disabled="busy || !selected['macos-arm64'] || !normalizedVersion" data-testid="upload-macos-zip" @click="upload('macos-arm64')">上传 macOS ZIP</button>
        <p v-if="currentDraft?.platforms['macos-arm64']" class="text-xs text-gray-500">已保存：{{ currentDraft.platforms['macos-arm64']?.filename }} · {{ formatSize(currentDraft.platforms['macos-arm64']?.size_bytes ?? 0) }}</p>
      </section>
    </div>

    <p v-if="uploadProgress" role="status" class="text-sm text-gray-500">{{ uploadProgress }}</p>

    <div class="flex flex-wrap items-center justify-between gap-4">
      <p class="max-w-2xl text-xs leading-6 text-gray-500">每个安装包最大 500 MB，上传最长等待 1 小时。安装包使用上方独立存储配置，支持本地或 OSS / R2。发行文件长期保留，不参与素材自动清理。</p>
      <button class="btn btn-primary" :disabled="busy || !canPublish" data-testid="publish-client-release" @click="publish">{{ busy ? '处理中…' : '发布此版本' }}</button>
    </div>

    <div class="border-t border-gray-200 pt-5 dark:border-gray-700">
      <div class="mb-3 flex items-center justify-between gap-4">
        <h3 class="font-semibold">历史版本</h3>
      </div>
      <p v-if="busy && !releases.length" class="text-sm text-gray-500">加载中…</p>
      <p v-else-if="!releases.length" class="text-sm text-gray-500">还没有客户端版本。</p>
      <div v-else class="divide-y divide-gray-200 dark:divide-gray-700">
        <article v-for="release in releases" :key="release.version" class="grid gap-3 py-4 sm:grid-cols-[110px_100px_1fr_auto] sm:items-start">
          <strong class="text-sm">v{{ release.version }}</strong>
          <span class="text-xs" :class="release.status === 'published' ? 'text-green-700 dark:text-green-400' : 'text-gray-500'">{{ release.version === latestPublishedVersion ? '当前版本' : release.status === 'published' ? '已发布' : release.status === 'deleting' ? '删除未完成' : '草稿' }}</span>
          <div class="space-y-1 text-xs text-gray-500">
            <p>Windows：{{ release.platforms['windows-x64'] ? `${release.platforms['windows-x64']?.filename} · ${formatSize(release.platforms['windows-x64']?.size_bytes ?? 0)}` : '未上传' }}</p>
            <p>macOS DMG：{{ release.platforms['macos-arm64-dmg'] ? `${release.platforms['macos-arm64-dmg']?.filename} · ${formatSize(release.platforms['macos-arm64-dmg']?.size_bytes ?? 0)}` : '未上传' }}</p>
            <p>macOS 更新 ZIP：{{ release.platforms['macos-arm64'] ? `${release.platforms['macos-arm64']?.filename} · ${formatSize(release.platforms['macos-arm64']?.size_bytes ?? 0)}` : '未上传' }}</p>
            <p v-if="release.notes">{{ release.notes }}</p>
          </div>
          <div class="flex flex-col items-start gap-2 sm:items-end">
            <button v-if="release.status === 'draft'" class="text-left text-sm font-medium text-primary-600 hover:text-primary-500 sm:text-right" :disabled="busy" @click="useDraft(release)">继续编辑</button>
            <span v-else class="text-xs text-gray-400 sm:text-right">{{ formatDate(release.published_at) }}</span>
            <button class="text-sm font-medium text-red-600 hover:text-red-500 disabled:cursor-not-allowed disabled:text-gray-400" :data-testid="`delete-release-${release.version}`" :disabled="busy || release.version === latestPublishedVersion" :title="release.version === latestPublishedVersion ? '请先发布新版本，再删除此版本' : undefined" @click="deleteTarget = release">{{ release.status === 'deleting' ? '重试删除' : '删除版本' }}</button>
          </div>
        </article>
      </div>
    </div>
  </section>
  <ConfirmDialog :show="Boolean(deleteTarget)" title="删除客户端版本" :message="`确认删除 v${deleteTarget?.version} 及其全部安装包？删除后无法恢复，当前最新版将保留。`" confirm-text="删除版本" cancel-text="取消" danger @confirm="removeRelease" @cancel="deleteTarget = null" />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import ClientReleaseStoragePanel from './ClientReleaseStoragePanel.vue'
import donebot, { type ClientRelease, type ClientReleasePlatform } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const showStorage = ref(false)
const app = useAppStore()
const stepUp = useStepUp()
const releases = ref<ClientRelease[]>([])
const deleteTarget = ref<ClientRelease | null>(null)
const latestPublishedVersion = computed(() => releases.value.find(release => release.status === 'published')?.version)
const version = ref('')
const notes = ref('')
const busy = ref(false)
const error = ref('')
const uploadProgress = ref('')
const selected = reactive<Record<ClientReleasePlatform, File | null>>({ 'windows-x64': null, 'macos-arm64': null, 'macos-arm64-dmg': null })
const windowsInput = ref<HTMLInputElement | null>(null)
const macDmgInput = ref<HTMLInputElement | null>(null)
const macZipInput = ref<HTMLInputElement | null>(null)
const normalizedVersion = computed(() => version.value.trim().replace(/^v/i, ''))
const currentDraft = computed(() => releases.value.find((release) => release.version === normalizedVersion.value && release.status === 'draft'))
const canPublish = computed(() => Boolean(currentDraft.value?.platforms['windows-x64'] && currentDraft.value?.platforms['macos-arm64'] && currentDraft.value?.platforms['macos-arm64-dmg']))

function selectFile(platform: ClientReleasePlatform, event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0] ?? null
  error.value = ''
  if (file && (file.size === 0 || file.size > 500_000_000)) {
    error.value = '安装包大小必须大于 0 且不超过 500 MB。'
    selected[platform] = null
    input.value = ''
    return
  }
  selected[platform] = file
}

async function load() {
  busy.value = true
  error.value = ''
  try {
    releases.value = (await donebot.clientReleases()).releases
  } catch (cause) {
    error.value = releaseErrorMessage(cause, '加载客户端版本失败')
  } finally {
    busy.value = false
  }
}

async function upload(platform: ClientReleasePlatform) {
  const file = selected[platform]
  if (!file || !normalizedVersion.value || busy.value) return
  busy.value = true
  error.value = ''
  uploadProgress.value = '正在上传安装包…'
  const form = new FormData()
  form.append('version', normalizedVersion.value)
  form.append('platform', platform)
  form.append('notes', notes.value)
  form.append('file', file)
  try {
    await stepUp.run(() => donebot.uploadClientRelease(form, (event) => {
      const total = event.total
      uploadProgress.value = total && event.loaded >= total
        ? '文件已发送，正在保存安装包，请稍候…'
        : total ? `正在上传安装包：${Math.floor(event.loaded / total * 100)}%` : '正在上传安装包…'
    }))
    selected[platform] = null
    const input = platform === 'windows-x64' ? windowsInput.value : platform === 'macos-arm64' ? macZipInput.value : macDmgInput.value
    if (input) input.value = ''
    const label = platform === 'windows-x64' ? 'Windows' : platform === 'macos-arm64' ? 'macOS ZIP' : 'macOS DMG'
    app.showSuccess(`${label} 安装包已保存`)
    await load()
  } catch (cause) {
    if (!isStepUpCancelled(cause)) error.value = releaseErrorMessage(cause, '上传安装包失败')
  } finally {
    uploadProgress.value = ''
    busy.value = false
  }
}

async function publish() {
  if (!canPublish.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await stepUp.run(() => donebot.publishClientRelease(normalizedVersion.value))
    app.showSuccess(`v${normalizedVersion.value} 已发布`)
    await load()
  } catch (cause) {
    if (!isStepUpCancelled(cause)) error.value = releaseErrorMessage(cause, '发布客户端版本失败')
  } finally {
    busy.value = false
  }
}

async function removeRelease() {
  const target = deleteTarget.value
  if (!target || busy.value || target.version === latestPublishedVersion.value) return
  deleteTarget.value = null
  busy.value = true
  error.value = ''
  try {
    await stepUp.run(() => donebot.deleteClientRelease(target.version))
    app.showSuccess(`v${target.version} 及其安装包已删除`)
    await load()
  } catch (cause) {
    if (!isStepUpCancelled(cause)) {
      await load()
      error.value = releaseErrorMessage(cause, '删除客户端版本失败')
    }
  } finally {
    busy.value = false
  }
}

function useDraft(release: ClientRelease) {
  version.value = release.version
  notes.value = release.notes ?? ''
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

function releaseErrorMessage(cause: unknown, fallback: string): string {
  if (!cause || typeof cause !== 'object') return fallback
  const failure = cause as { status?: number; code?: string; message?: unknown }
  if (failure.status === 413) return '上传请求超过服务器限制（HTTP 413）。安装包最大 500 MB；请检查反向代理对此上传路径的大小限制，并预留表单开销。'
  if (failure.code === 'ECONNABORTED' || failure.code === 'ETIMEDOUT') return '上传超时（最长等待 1 小时），请检查网络后重试。'
  if (failure.status === 0) return '上传连接中断，请检查网络及反向代理连接后重试。'
  const message = typeof failure.message === 'string' ? failure.message.trim() : ''
  return message ? `${fallback}：${message}${failure.status ? `（HTTP ${failure.status}）` : ''}` : fallback
}

function formatSize(size: number) { return size > 0 ? `${(size / 1_000_000).toFixed(1)} MB` : '—' }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN') : '' }

onMounted(load)
</script>
