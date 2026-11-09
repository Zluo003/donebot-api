<template>
  <AppLayout>
    <div class="min-w-0 space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h1 class="text-2xl font-bold">DoneBot 模板库</h1>
        <button class="btn btn-primary gap-2" :disabled="busy" @click="openPublish"><Icon name="plus" size="sm" />发布模板</button>
      </div>
      <div class="flex flex-wrap items-center gap-3">
        <label class="relative min-w-0 flex-1 sm:max-w-sm"><span class="sr-only">搜索模板</span><input v-model="search" class="input" placeholder="搜索名称、岗位或分类"></label>
        <select v-model="kind" class="input w-auto" aria-label="模板类型"><option value="">全部类型</option><option value="bot">机器人</option><option value="department">部门</option></select>
        <button class="btn btn-secondary" :disabled="busy || loading" title="刷新" aria-label="刷新" @click="load"><Icon name="refresh" size="sm" /></button>
      </div>
      <p v-if="error" role="alert" class="break-words text-sm text-red-600">{{ error }}</p>
      <p v-if="loading" role="status" class="py-12 text-center text-sm text-gray-500">加载中…</p>
      <p v-else-if="!visible.length" class="py-16 text-center text-sm text-gray-500">{{ entries.length ? '没有匹配的模板' : '尚未发布模板' }}</p>
      <div v-else class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700">
        <article v-for="entry in visible" :key="`${entry.id}:${entry.release}`" class="flex min-w-0 flex-wrap items-center gap-4 py-4">
          <div class="min-w-0 flex-1 basis-64">
            <div class="flex flex-wrap items-center gap-2"><h2 class="break-words font-semibold">{{ entry.name }}</h2><span class="text-xs text-gray-500">{{ entry.kind === 'bot' ? '机器人' : '部门' }} · v{{ entry.release }}</span><span class="text-xs" :class="entry.published ? 'text-emerald-600' : 'text-gray-500'">{{ entry.published ? '已上架' : '已下架' }}</span></div>
            <p class="mt-1 break-words text-sm text-gray-500">{{ entry.summary }}</p>
            <p class="mt-2 text-xs text-gray-500">{{ entry.category }} · {{ entry.members.length }} 个岗位 · {{ entry.skills.length }} 个技能</p>
          </div>
          <div class="flex shrink-0 gap-2">
            <button class="btn btn-secondary" :disabled="busy" @click="details(entry)">查看</button>
            <button class="btn btn-secondary" :disabled="busy" @click="toggle(entry)">{{ entry.published ? '下架' : '上架' }}</button>
          </div>
        </article>
      </div>
    </div>
    <BaseDialog :show="show" :title="readonly ? '模板详情' : '发布模板'" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
      <div class="min-w-0 space-y-5">
        <label v-if="!readonly" class="block text-sm"><span class="mb-2 block font-medium">模板包</span><input type="file" accept=".json,application/json" :disabled="busy" aria-label="选择模板包" @change="upload"></label>
        <p v-if="dialogError" role="alert" class="break-words text-sm text-red-600">{{ dialogError }}</p>
        <div v-if="draft && preview" class="space-y-4">
          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block text-sm"><span class="mb-1 block">名称</span><input v-model="draft.package.name" class="input" maxlength="100" :disabled="busy || readonly" aria-label="模板名称"></label>
            <label class="block text-sm"><span class="mb-1 block">版本</span><input v-model="draft.package.release" class="input" :disabled="busy || readonly" aria-label="模板版本" placeholder="1.0.0"></label>
          </div>
          <label class="block text-sm"><span class="mb-1 block">简介</span><textarea v-model="draft.package.summary" class="input min-h-24" maxlength="2000" :disabled="busy || readonly" aria-label="模板简介" /></label>
          <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
            <div class="mb-3 flex flex-wrap gap-4 text-sm"><strong>{{ preview.kind === 'bot' ? '机器人模板' : '部门模板' }}</strong><span>{{ preview.members.length }} 个岗位</span><span>{{ preview.skills.length }} 个技能</span></div>
            <p class="break-words text-sm">{{ preview.members.join('、') }}</p>
            <details class="mt-4"><summary class="cursor-pointer text-sm">人设与技能内容</summary><pre class="mt-3 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ contentPreview }}</pre></details>
          </div>
        </div>
      </div>
      <template #footer><button class="btn btn-secondary" :disabled="busy" @click="close">关闭</button><button v-if="readonly" class="btn btn-primary" :disabled="busy" @click="newVersion">发布新版本</button><button v-else class="btn btn-primary" :disabled="busy || !draft" @click="publish">{{ busy ? '处理中…' : '发布' }}</button></template>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import library, { type TemplateDocument, type TemplateEntry, type TemplateRelease } from '@/api/admin/donebot-templates'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const stepUp = useStepUp()
const entries = ref<TemplateEntry[]>([])
const search = ref('')
const kind = ref('')
const busy = ref(false)
const loading = ref(true)
const error = ref('')
const dialogError = ref('')
const show = ref(false)
const readonly = ref(false)
const draft = ref<TemplateDocument | null>(null)
const preview = ref<TemplateRelease | null>(null)
const visible = computed(() => entries.value.filter(entry => (!kind.value || entry.kind === kind.value) && [entry.name, entry.category, entry.summary, ...entry.members].join(' ').toLowerCase().includes(search.value.trim().toLowerCase())))
const contentPreview = computed(() => draft.value ? JSON.stringify({ agents: draft.value.package.agents, presets: draft.value.package.presets, skills: draft.value.package.skills }, null, 2) : '')
function message(cause: unknown) { const value = cause as { response?: { data?: { message?: string } }; message?: string }; return value?.response?.data?.message || value?.message || '操作失败，请重试' }
async function load() {
  loading.value = true; error.value = ''
  try { entries.value = await library.list() } catch (cause) { error.value = message(cause) } finally { loading.value = false }
}
function openPublish() { readonly.value = false; draft.value = null; preview.value = null; dialogError.value = ''; show.value = true }
function close() { if (!busy.value) show.value = false }
function newVersion() {
  if (!draft.value) return
  readonly.value = false
  const parts = draft.value.package.release.split('.').map(Number)
  draft.value.package.release = `${parts[0]}.${parts[1]}.${(parts[2] ?? 0) + 1}`
}
async function upload(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  draft.value = null; preview.value = null; dialogError.value = ''
  if (!file) return
  if (file.size > 4 * 1024 * 1024) { dialogError.value = '模板包不能超过 4 MB'; return }
  busy.value = true
  try { preview.value = await library.preview(JSON.parse(await file.text())); draft.value = preview.value.document } catch (cause) { dialogError.value = message(cause) } finally { busy.value = false }
}
async function publish() {
  if (!draft.value || busy.value) return
  busy.value = true; dialogError.value = ''
  const document = JSON.parse(JSON.stringify(draft.value)) as TemplateDocument
  try { await stepUp.run(() => library.publish(document)); show.value = false; app.showSuccess('模板已发布'); await load() } catch (cause) { if (!isStepUpCancelled(cause)) dialogError.value = message(cause) } finally { busy.value = false }
}
async function details(entry: TemplateEntry) {
  busy.value = true; error.value = ''
  try { preview.value = await library.get(entry); draft.value = preview.value.document; readonly.value = true; dialogError.value = ''; show.value = true } catch (cause) { error.value = message(cause) } finally { busy.value = false }
}
async function toggle(entry: TemplateEntry) {
  busy.value = true; error.value = ''
  try { await stepUp.run(() => library.visibility(entry, !entry.published)); await load() } catch (cause) { if (!isStepUpCancelled(cause)) error.value = message(cause) } finally { busy.value = false }
}
onMounted(load)
</script>
