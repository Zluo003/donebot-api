<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">DoneBot Cloud</h1>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">专属云实例，使用当前 DoneBot 账号与余额。</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading || busy" @click="load">刷新</button>
      </header>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-xl bg-primary-50 p-4 text-sm text-primary-700 dark:bg-dark-800 dark:text-primary-300">{{ notice }}</p>
      <p v-if="loading && !info" role="status" class="py-12 text-center text-gray-500">正在加载 Cloud…</p>
      <template v-if="info">
        <section class="card grid gap-5 p-6 sm:grid-cols-3" aria-label="Cloud 套餐">
          <div><p class="text-sm text-gray-500 dark:text-dark-400">月费（账户余额单位）</p><p class="mt-2 text-2xl font-semibold">{{ info.plan.monthly_price }}<span class="ml-1 text-sm font-normal">/ 月</span></p></div>
          <div><p class="text-sm text-gray-500 dark:text-dark-400">可用余额</p><p class="mt-2 text-2xl font-semibold">{{ auth.user?.balance ?? '—' }}</p></div>
          <div><p class="text-sm text-gray-500 dark:text-dark-400">到期停机后保留</p><p class="mt-2 text-2xl font-semibold">{{ info.plan.retention_days }}<span class="ml-1 text-sm font-normal">天</span></p></div>
          <p class="text-sm leading-6 text-gray-500 sm:col-span-3 dark:text-dark-400">按月预付，到期从账户余额自动续费。余额不足不开通、不续期；到期停机后，保留期结束将永久删除实例与数据。保留期内补足余额后会尝试续费恢复。</p>
        </section>
        <section v-if="!instance" class="card space-y-4 p-8 text-center">
          <h2 class="text-xl font-semibold">你的专属 DoneBot</h2>
          <p class="text-sm text-gray-500 dark:text-dark-400">聊天、文件、记忆和配置保存在你的独立实例中。</p>
          <button class="btn btn-primary" :disabled="busy || !info.plan.enabled" @click="confirm('start')">开通 Cloud</button>
          <p v-if="!info.plan.enabled" class="text-sm text-gray-500">Cloud 暂未开放开通。</p>
        </section>
        <section v-else class="card space-y-5 p-6" aria-label="我的 Cloud 实例">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div><h2 class="text-lg font-semibold">我的 Cloud</h2><p class="mt-1 text-sm text-gray-500 dark:text-dark-400" role="status">{{ status }}</p></div>
            <span class="rounded-full bg-gray-100 px-3 py-1 text-sm dark:bg-dark-700">{{ instance.subscription_state === 'active' ? '已预付' : instance.subscription_state === 'cancelled' ? '已取消续费' : '等待开通' }}</span>
          </div>
          <p class="text-sm text-gray-500 dark:text-dark-400">已付费至：{{ date(instance.paid_until) }}</p>
          <p v-if="instance.purge_locked" class="text-sm text-red-600">保留期已结束，实例正在永久删除，不能再续费恢复。</p>
          <p v-if="operationFailure" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ operationFailure }}</p>
          <div class="flex flex-wrap gap-3">
            <button class="btn btn-primary" :disabled="busy || !canOpen || !info.browser_available" @click="open">打开云实例</button>
            <button class="btn btn-secondary" :disabled="busy || !canOpen || !info.browser_available" @click="desktop">在 DoneBot 客户端打开</button>
            <button v-if="(instance.desired !== 'running' || failedStart) && !instance.purge_locked" class="btn btn-secondary" :disabled="busy" @click="confirm('start')">{{ failedStart ? '重试开通' : instance.subscription_state === 'cancelled' ? '重新开通' : '启动' }}</button>
            <button v-if="instance.desired === 'running'" class="btn btn-secondary" :disabled="busy" @click="confirm('stop')">停机并保留数据</button>
            <button v-if="instance.subscription_state !== 'cancelled' && !instance.purge_locked" class="btn btn-secondary" :disabled="busy" @click="confirm('cancel')">取消自动续费</button>
          </div>
          <p v-if="!info.browser_available" class="text-sm text-gray-500">网页连接入口尚未配置，请联系管理员。</p>
          <p class="text-sm text-gray-500 dark:text-dark-400">停机仍保留已付周期及自动续费；取消自动续费会立即停机，本月已激活费用不退还。</p>
        </section>
        <CloudStorage v-if="instance && canOpen" :key="instance.id" :id="instance.id" @busy="busy = $event" />
        <button class="text-sm text-gray-500 underline dark:text-dark-400" :disabled="busy" @click="revoke">断开所有 Cloud 网页会话</button>
      </template>
      <BaseDialog v-if="pending" :show="true" :title="pending.action === 'cancel' ? '取消自动续费？' : pending.action === 'stop' ? '停机并保留数据？' : instance ? '启动 Cloud？' : '开通 Cloud？'" width="narrow" :close-on-escape="!busy" :show-close-button="!busy" @close="!busy && (pending = null)">
          <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ pending.action === 'cancel' ? '实例将立即停机并关闭自动续费。本月已激活费用不退还；到期及保留期结束后，实例与数据将永久删除。' : pending.action === 'stop' ? '关闭当前连接并停止实例，保留独立数据卷。已付周期及自动续费继续生效。' : instance?.subscription_state === 'cancelled' ? `重新开通将从余额扣除月费 ${info?.plan.monthly_price}，开始新的一个月，并恢复自动续费。余额不足时不能开通。` : `月费 ${info?.plan.monthly_price}，从当前账号余额预付扣款。已付周期内启动不重复收取月费；余额不足时无法开通或续期。` }}</p>
          <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
          <template #footer><div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="busy" @click="pending = null">返回</button><button class="btn btn-primary" :disabled="busy" @click="submit">{{ busy ? '正在处理…' : '确认' }}</button></div></template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CloudStorage from '@/components/CloudStorage.vue'
import { useAuthStore } from '@/stores/auth'
import { cloudAPI, type CloudAction, type CloudInstance, type CloudOperation, type CloudPlanInfo } from '@/api/cloud'
const auth = useAuthStore()
const info = ref<CloudPlanInfo | null>(null), instance = ref<CloudInstance | null>(null), operations = ref<CloudOperation[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const pending = ref<{ action: CloudAction; key: string } | null>(null)
let alive = true, timer: ReturnType<typeof setInterval> | undefined
const describe = (e: unknown) => (e as { message?: string })?.message || '请求失败，请刷新后重试。'
const canOpen = computed(() => !!instance.value && instance.value.actual === 'running' && instance.value.desired === 'running' && instance.value.subscription_state === 'active' && !instance.value.purge_locked && Date.parse(instance.value.paid_until || '') > Date.now())
const failedStart = computed(() => instance.value?.actual === 'error' && operations.value[0]?.action === 'start' && operations.value[0]?.state === 'failed')
const operationFailure = computed(() => {
  const code = instance.value?.error || (operations.value[0]?.state === 'failed' ? operations.value[0].error : '')
  if (!code) return ''
  if (code === 'billing_insufficient_balance') return '账户余额不足，Cloud 未开通且未扣款。补足余额后可重试开通。'
  if (code === 'billing_failed') return '开通扣款未完成，请检查账户余额或稍后重试。'
  if (code === 'capacity_unavailable') return '暂时没有可用节点，Cloud 未开通且未扣款，请稍后重试。'
  return '最近的操作未完成。请刷新查看恢复结果；数据删除成功前不会显示为已删除。'
})
const status = computed(() => {
  const value = instance.value
  if (!value) return ''
  if (value.purge_locked) return '正在删除'
  if (Date.parse(value.paid_until || '') <= Date.now()) return '已到期，等待续费或回收'
  if (value.desired === 'stopped' && value.actual === 'stopped') return '已停机，数据保留中'
  if (canOpen.value) return '运行中'
  return value.actual === 'error' ? '需要恢复' : '正在准备，请稍候'
})
const date = (value: string | null) => value ? new Date(value).toLocaleString() : '尚未开通'
async function load() {
  if (loading.value) return
  loading.value = true
  try {
    const [plan, instances] = await Promise.all([cloudAPI.plan(), cloudAPI.instances()])
    const current = instances[0] ?? null
    const recent = current ? await cloudAPI.operations(current.id) : []
    if (alive) { info.value = plan; instance.value = current; operations.value = recent; error.value = '' }
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) loading.value = false }
}
function confirm(action: CloudAction) { error.value = ''; notice.value = ''; pending.value = { action, key: crypto.randomUUID() } }
async function submit() {
  if (!pending.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await cloudAPI.mutate(instance.value?.id, pending.value.action, pending.value.key)
    if (!alive) return
    pending.value = null; notice.value = result?.operation.state === 'failed' ? '' : '操作已受理，状态将自动更新。'
    await Promise.all([load(), auth.refreshUser().catch(() => {})])
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}
async function open() {
  if (!instance.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await cloudAPI.launch(instance.value.id), url = new URL(result.url)
    if ((url.protocol !== 'https:' && !(import.meta.env.DEV && url.protocol === 'http:' && url.hostname.endsWith('.cloud.localhost'))) || !url.hostname.startsWith(instance.value.id + '.') || url.pathname !== '/__cloud/launch' || url.search || url.username || url.password || !url.hash.startsWith('#code=')) throw new Error('网页连接地址无效，请联系管理员。')
    if (alive) window.location.assign(url.href)
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}
async function revoke() {
  busy.value = true; error.value = ''
  try { await cloudAPI.revoke(); if (alive) notice.value = '已断开 Cloud 网页会话，实例继续按当前状态运行。' }
  catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}
async function desktop() {
  if (!instance.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await cloudAPI.desktop(instance.value.id), url = new URL(result.url)
    const address = new URL(url.searchParams.get('address') || '')
    if (url.protocol !== 'donebot:' || url.hostname !== 'pair' || !address.hostname.startsWith(instance.value.id + '.') || address.protocol !== 'https:' || address.pathname !== '/' || address.username || address.password || address.hash || address.search || !/^omb_pair_[A-Za-z0-9_-]{43}$/.test(url.searchParams.get('token') || '')) throw new Error('客户端连接地址无效。')
    if (alive) window.location.assign(url.href)
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}
onMounted(() => { void load(); timer = setInterval(() => { if (!busy.value && !pending.value) void load() }, 3000) })
onUnmounted(() => { alive = false; clearInterval(timer) })
</script>
