<template>
  <section class="card" aria-labelledby="devices-title">
    <header class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div><h2 id="devices-title" class="text-lg font-medium">{{ t('mobileDevices.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('mobileDevices.description') }}</p></div>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</button>
    </header>
    <div class="space-y-4 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ t('mobileDevices.error') }}</p>
      <p v-if="devices && !devices.relay_configured" role="status" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ t('mobileDevices.unconfigured') }}</p>
      <p v-if="!devices && loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-if="devices" class="grid gap-6 md:grid-cols-2">
        <section v-for="kind in kinds" :key="kind" :aria-label="t(`mobileDevices.${kind}`)">
          <h3 class="mb-3 font-medium">{{ t(`mobileDevices.${kind}`) }} <span class="text-sm text-gray-400">{{ devices[kind].length }}</span></h3>
          <ul class="space-y-3">
            <li v-for="device in devices[kind]" :key="device.id" class="rounded-xl border border-gray-100 p-4 dark:border-dark-700">
              <div class="flex items-start justify-between gap-3"><div class="min-w-0"><p class="break-words font-medium">{{ device.name }}</p><p class="mt-1 text-xs text-gray-500">{{ platform(device.platform, kind) }}</p></div><span class="shrink-0 rounded-full px-2 py-1 text-xs" :class="online(device) ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'">{{ status(device, kind) }}</span></div>
              <p class="mt-3 text-xs text-gray-500">{{ t('mobileDevices.lastSeen') }}：{{ device.last_seen ? new Date(device.last_seen).toLocaleString() : t('mobileDevices.neverSeen') }}</p>
            </li>
          </ul>
          <p v-if="!devices[kind].length" class="rounded-xl bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-800">{{ t(`mobileDevices.empty${kind}`) }}</p>
        </section>
      </div>
      <p class="text-xs leading-relaxed text-gray-500">{{ t('mobileDevices.note') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getDoneBotDevices, type DoneBotDevice, type DoneBotDevices } from '@/api/devices'
const { t } = useI18n()
const kinds = ['desktop', 'mobile'] as const
const devices = ref<DoneBotDevices | null>(null)
const loading = ref(false)
const error = ref(false)
const now = ref(Date.now())
let active = true
let timer: ReturnType<typeof setInterval> | undefined
async function refresh() {
  if (loading.value) return
  loading.value = true
  try { const next = await getDoneBotDevices(); if (active) { devices.value = next; error.value = false; now.value = Date.now() } }
  catch { if (active) error.value = true }
  finally { if (active) loading.value = false }
}
function online(device: DoneBotDevice) { return !error.value && device.online && device.last_seen !== null && now.value - Date.parse(device.last_seen) < 45_000 }
function status(device: DoneBotDevice, kind: string) {
  return t(`mobileDevices.${!online(device) ? kind === 'mobile' ? 'background' : 'offline' : kind === 'mobile' ? 'online' : device.connection === 'connected' ? 'ready' : 'connecting'}`)
}
function platform(value: string, kind: string) {
  if (kind === 'desktop') return value.startsWith('darwin') ? 'macOS' : value.startsWith('win') ? 'Windows' : value.startsWith('linux') ? 'Linux' : t('mobileDevices.desktopAuthorized')
  return value === 'ios' ? 'iOS' : value === 'android' ? 'Android' : value === 'harmony' ? 'HarmonyOS' : 'Web'
}
onMounted(() => { void refresh(); timer = setInterval(() => { now.value = Date.now(); if (!document.hidden) void refresh() }, 15_000) })
onUnmounted(() => { active = false; clearInterval(timer) })
</script>
