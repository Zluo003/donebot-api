<template>
  <section class="card space-y-4 p-6" aria-label="Cloud 存储与备份">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">存储与备份</h2><button class="btn btn-secondary" :disabled="busy" @click="load">刷新存储</button></div>
    <p v-if="resources" class="text-sm text-gray-500">{{ resources.cpu_cores }} 核 CPU · {{ size(resources.memory_bytes) }} 内存 · {{ resources.storage.enforced ? size(resources.storage.limit_bytes) + ' 磁盘上限' : '未启用磁盘配额' }}<span v-if="resources.storage.available_bytes !== undefined"> · 剩余 {{ size(resources.storage.available_bytes) }}</span></p>
    <p v-if="resources?.desktop?.enabled" class="text-sm text-gray-500">专属桌面按需启动：{{ resources.desktop.cpu_cores }} 核 CPU · {{ size(resources.desktop.memory_bytes) }} 内存 · {{ size(resources.desktop.storage.limit_bytes) }} 资料盘<span v-if="resources.desktop.storage.available_bytes !== undefined"> · 剩余 {{ size(resources.desktop.storage.available_bytes) }}</span> · 空闲 {{ Math.round(resources.desktop.idle_timeout_seconds / 60) }} 分钟后停止并保留资料。备份同时包含实例和桌面数据。</p>
    <p v-if="resources?.storage.available_bytes !== undefined && resources.storage.available_bytes < 64 * 1024 ** 2" role="alert" class="text-sm text-amber-700">磁盘空间不足，请先清理实例内文件，避免聊天和配置保存失败。</p>
    <p class="text-sm leading-6 text-gray-500">备份和恢复会短暂停机并断开连接。最多保留 3 份加密备份，保存在当前服务器；恢复会覆盖当前实例数据。服务保留期结束后，备份随实例一起删除。</p>
    <p v-if="resources?.backups?.automatic_interval_hours" class="text-sm text-gray-500">管理员已启用每 {{ resources.backups.automatic_interval_hours }} 小时自动备份，保留最新 3 份，最旧备份会自动删除。停机期间不自动备份。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-primary-700">{{ notice }}</p>
    <button class="btn btn-secondary" :disabled="busy || backups.length >= 3" @click="prepare('create')">创建备份</button>
    <ul class="space-y-2">
      <li v-for="item in backups" :key="item.id" class="flex flex-wrap items-center gap-3 text-sm">
        <span>{{ new Date(item.created_at).toLocaleString() }} · {{ item.state === 'ready' ? '可恢复' : item.state === 'failed' ? '备份失败' : '备份中或等待恢复' }}</span>
        <button v-if="item.state === 'ready'" class="btn btn-secondary" :disabled="busy" @click="prepare('restore', item.id)">恢复此备份</button>
        <button class="btn btn-secondary" :disabled="busy" @click="prepare('delete', item.id)">删除备份</button>
      </li>
    </ul>
    <BaseDialog v-if="pending" :show="true" :title="pending.action === 'restore' ? '覆盖当前数据并恢复？' : pending.action === 'delete' ? '永久删除此备份？' : '停机并创建备份？'" :close-on-escape="!busy" :show-close-button="!busy" width="narrow" @close="!busy && (pending = null)">
      <p class="text-sm text-gray-500">{{ pending.action === 'restore' ? '当前聊天、文件、记忆、配置和专属桌面资料将被备份中的数据覆盖。实例会短暂停机，完成后重新运行；桌面在下次使用时启动。' : pending.action === 'delete' ? '仅删除选中的备份，不能撤销。' : '实例和桌面将短暂停机以保存一致的数据，备份完成后实例重新运行。' }}</p>
      <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
      <template #footer><button class="btn btn-secondary" :disabled="busy" @click="pending = null">返回</button><button class="btn btn-primary" :disabled="busy" @click="submit">{{ busy ? '正在处理…' : '确认' }}</button></template>
    </BaseDialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { cloudAPI, type CloudBackup, type CloudResources } from '@/api/cloud'
const props = defineProps<{ id: string }>()
const emit = defineEmits<{ busy: [value: boolean] }>()
const resources = ref<CloudResources | null>(null), backups = ref<CloudBackup[]>([]), error = ref(''), notice = ref(''), busy = ref(false)
const pending = ref<{ action: 'create' | 'restore' | 'delete'; snapshot?: string; key: string } | null>(null)
let alive = true, loading = false, timer: ReturnType<typeof setInterval> | undefined
const size = (value: number | null) => value === null ? '—' : (value / 1024 ** 3).toFixed(2) + ' GiB'
async function load() {
  if (loading) return
  loading = true
  try { const [r, b] = await Promise.all([cloudAPI.resources(props.id), cloudAPI.backups(props.id)]); if (alive) { resources.value = r; backups.value = b; error.value = '' } }
  catch { if (alive) error.value = '存储信息暂不可用，请稍后刷新。' }
  finally { loading = false }
}
function prepare(action: 'create' | 'restore' | 'delete', snapshot?: string) { pending.value = { action, snapshot, key: crypto.randomUUID() }; error.value = ''; notice.value = '' }
async function submit() {
  if (!pending.value || busy.value) return
  busy.value = true; emit('busy', true); error.value = ''
  try { const p = pending.value; await cloudAPI.backup(props.id, p.action, p.key, p.snapshot); if (alive) { pending.value = null; notice.value = '操作完成。'; await load() } }
  catch { if (alive) error.value = '操作未确认完成。请刷新检查状态，或使用当前确认窗口重试。' }
  finally { busy.value = false; emit('busy', false) }
}
onMounted(() => { void load(); timer = setInterval(() => { if (!busy.value && !pending.value) void load() }, 15000) })
onUnmounted(() => { alive = false; clearInterval(timer) })
</script>
