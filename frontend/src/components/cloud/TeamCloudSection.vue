<template>
  <section v-if="enabled" class="space-y-5 pt-4 border-t border-gray-200 dark:border-dark-700" aria-label="团队协作 Cloud">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-xl font-semibold text-gray-900 dark:text-white">团队 Cloud</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">团队使用独立 Cloud 实例与共享文件空间，个人资料和账单保持独立。</p>
      </div>
      <button class="btn btn-secondary" :disabled="loading || busy" @click="loadAll">刷新团队</button>
    </header>

    <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="rounded-xl bg-primary-50 p-4 text-sm text-primary-700 dark:bg-dark-800 dark:text-primary-300">{{ notice }}</p>

    <div class="card space-y-5 p-6">
      <div class="grid gap-4 sm:grid-cols-2">
        <form class="flex gap-2" @submit.prevent="createTeam">
          <input v-model.trim="newTeamName" class="input flex-1" placeholder="输入团队名称" maxlength="100" required :disabled="busy" aria-label="新建团队名称" />
          <button type="submit" class="btn btn-secondary shrink-0" :disabled="busy || !newTeamName">创建团队</button>
        </form>
        <form class="flex gap-2" @submit.prevent="acceptInvite">
          <input v-model.trim="inviteToken" class="input flex-1" placeholder="粘贴 43 位邀请码" maxlength="43" required :disabled="busy" aria-label="团队邀请码" />
          <button type="submit" class="btn btn-secondary shrink-0" :disabled="busy || !/^[A-Za-z0-9_-]{43}$/.test(inviteToken)">加入团队</button>
        </form>
      </div>

      <div v-if="teams.length > 0" class="space-y-5">
        <label class="block text-sm font-medium">
          当前团队
          <select v-model="selectedId" class="input mt-1.5 w-full" :disabled="busy" aria-label="选择团队">
            <option v-for="item in teams" :key="item.id" :value="item.id">{{ item.name }} · {{ roleText[item.role] }}</option>
          </select>
        </label>

        <div v-if="selectedTeam && cloud" class="space-y-4 rounded-xl border border-gray-200 p-5 dark:border-dark-700">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold">{{ selectedTeam.name }} · 团队云主机</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400" role="status">{{ cloudStatus }}</p>
            </div>
            <span v-if="instance" class="rounded-full bg-gray-100 px-3 py-1 text-xs dark:bg-dark-700">
              {{ !paid ? '服务已到期' : instance.auto_renew ? '自动续费已开启' : '自动续费已关闭' }}
            </span>
          </div>

          <template v-if="instance">
            <p class="text-sm text-gray-500 dark:text-dark-400">
              已付费至：{{ formatDate(instance.paid_until) }}
              <span v-if="instance.plan_name" class="ml-3">规格：{{ instance.plan_name }}</span>
            </p>
            <p v-if="instance.purge_locked" class="text-sm text-red-600">保留期已结束，团队实例正在永久删除，无法恢复。</p>
            <p v-if="operationFailure" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ operationFailure }}</p>
            <div class="flex flex-wrap gap-3">
              <button class="btn btn-primary" :disabled="busy || !canOpen || !cloud.connection_available" @click="openBrowser">打开团队云实例</button>
              <button class="btn btn-secondary" :disabled="busy || !canOpen || !cloud.connection_available" @click="openDesktop">在 DoneBot 客户端打开</button>
              <template v-if="selectedTeam.role === 'owner' && !instance.purge_locked">
                <button v-if="!paid || instance.desired !== 'running' || failedStart" class="btn btn-secondary" :disabled="busy" @click="confirmCloud('start')">{{ failedStart ? '重试开通' : !renewable ? '续费并启动' : '启动团队 Cloud' }}</button>
                <button v-if="renewable" class="btn btn-secondary" :disabled="busy" @click="confirmCloud(instance.auto_renew ? 'cancel' : 'keep')">{{ instance.auto_renew ? '取消自动续费' : '开启自动续费' }}</button>
              </template>
            </div>
          </template>

          <template v-else>
            <template v-if="selectedTeam.role === 'owner'">
              <p v-if="plans.length === 0" class="text-sm text-gray-500">管理员尚未配置可售团队 Cloud 套餐。</p>
              <div v-else class="space-y-3">
                <div class="grid gap-3 sm:grid-cols-2">
                  <label v-for="plan in plans" :key="plan.id" class="flex cursor-pointer flex-col gap-1 rounded-xl border p-4 text-sm transition" :class="selectedPlanId === plan.id ? 'border-primary-500 bg-primary-50/40 dark:bg-dark-800' : 'border-gray-200 dark:border-dark-700'">
                    <div class="flex items-center justify-between gap-2">
                      <span class="font-semibold">{{ plan.name }}</span>
                      <input v-model="selectedPlanId" type="radio" :value="plan.id" name="team-cloud-plan" />
                    </div>
                    <p class="text-base font-semibold">{{ plan.monthly_price }} <span class="text-xs font-normal text-gray-500">/ 月</span></p>
                    <p class="text-xs text-gray-500 dark:text-dark-400">
                      运行 {{ (plan.runtime.cpu_millis / 1000).toFixed(1) }} 核 / {{ (plan.runtime.memory_mib / 1024).toFixed(1) }} GiB · 共享文件 {{ formatBytes(plan.shared_quota_bytes) }} · 保留 {{ plan.retention_days }} 天
                    </p>
                  </label>
                </div>
                <button class="btn btn-primary" :disabled="busy || !chosenPlan" @click="confirmCloud('start')">为团队购买 Cloud 云主机</button>
              </div>
            </template>
            <p v-else class="text-sm text-gray-500 dark:text-dark-400">团队 Cloud 尚未开通，请联系团队所有者开通后使用。</p>
          </template>
        </div>

        <div v-if="selectedTeam" class="space-y-3">
          <h3 class="text-sm font-semibold">团队成员（{{ members.length }}）</h3>
          <ul class="divide-y divide-gray-200 dark:divide-dark-700">
            <li v-for="member in members" :key="member.user_id" class="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
              <span>{{ member.email }} · {{ roleText[member.role] }}</span>
              <div v-if="member.role !== 'owner' && (selectedTeam.role === 'owner' || (selectedTeam.role === 'admin' && member.role === 'member'))" class="flex gap-2">
                <button v-if="selectedTeam.role === 'owner'" class="btn btn-secondary btn-sm" :disabled="busy" @click="changeRole(member)">{{ member.role === 'admin' ? '设为成员' : '设为管理员' }}</button>
                <button class="btn btn-secondary btn-sm" :disabled="busy" @click="removeMember(member)">移除成员</button>
              </div>
            </li>
          </ul>
          <form v-if="selectedTeam.role !== 'member'" class="flex flex-wrap gap-2 pt-2" @submit.prevent="sendInvite">
            <input v-model.trim="inviteEmail" type="email" class="input flex-1" placeholder="输入成员邮箱生成邀请码" maxlength="254" required :disabled="busy" aria-label="邀请成员邮箱" />
            <button type="submit" class="btn btn-secondary" :disabled="busy || !inviteEmail">生成邀请</button>
          </form>
          <div v-if="lastInvite" class="space-y-2 rounded-xl border border-gray-200 p-3 text-sm dark:border-dark-700">
            <p>请将邀请码发送给 {{ lastInvite.email }}（有效期至 {{ formatDate(lastInvite.expires_at) }}）：</p>
            <div class="flex gap-2">
              <input :value="lastInvite.token" readonly class="input flex-1 font-mono text-xs" aria-label="生成的邀请码" />
              <button class="btn btn-secondary" :disabled="busy" @click="revokeInvite">撤销邀请</button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <BaseDialog v-if="pending" :show="true" :title="dialogTitle" width="narrow" :close-on-escape="!busy" :show-close-button="!busy" @close="!busy && (pending = null)">
      <p class="text-sm leading-6 text-gray-500 dark:text-dark-400">{{ dialogBody }}</p>
      <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" :disabled="busy" @click="pending = null">返回</button>
          <button class="btn btn-primary" :disabled="busy" @click="submitCloud">{{ busy ? '正在处理…' : '确认' }}</button>
        </div>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAuthStore } from '@/stores/auth'
import type { CloudAction } from '@/api/cloud'
import { teamsAPI, type TeamCloudDetail, type TeamCloudPlan, type TeamIdentity, type TeamInvitation, type TeamMember, type TeamRole } from '@/api/teams'

const auth = useAuthStore()
const enabled = ref(false), loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const teams = ref<TeamIdentity[]>([]), plans = ref<TeamCloudPlan[]>([]), members = ref<TeamMember[]>([])
const selectedId = ref(''), selectedPlanId = ref('')
const cloud = ref<TeamCloudDetail | null>(null)
const newTeamName = ref(''), inviteToken = ref(''), inviteEmail = ref('')
const lastInvite = ref<TeamInvitation | null>(null)
const pending = ref<{ action: CloudAction; key: string; planId?: string } | null>(null)
let alive = true, timer: ReturnType<typeof setInterval> | undefined, createKey: { name: string; key: string } | null = null

const roleText: Record<TeamRole, string> = { owner: '所有者', admin: '管理员', member: '成员' }
const describe = (e: unknown) => (e as { message?: string })?.message || '团队请求失败，请刷新后重试。'
const formatDate = (v: string | null) => (v ? new Date(v).toLocaleString() : '尚未开通')
const formatBytes = (n: number) => (n >= 1024 ** 3 ? `${(n / 1024 ** 3).toFixed(1)} GiB` : `${Math.round(n / 1024 ** 2)} MiB`)

const selectedTeam = computed(() => teams.value.find(item => item.id === selectedId.value) ?? null)
const instance = computed(() => cloud.value?.instance ?? null)
const chosenPlan = computed(() => plans.value.find(p => p.id === selectedPlanId.value) ?? null)
const paid = computed(() => !!instance.value?.paid_until && Date.parse(instance.value.paid_until) > Date.now())
const renewable = computed(() => paid.value && ['active', 'pending'].includes(instance.value?.subscription_state ?? ''))
const canOpen = computed(() => !!instance.value && instance.value.actual === 'running' && instance.value.desired === 'running' && instance.value.subscription_state === 'active' && !instance.value.purge_locked && paid.value)
const failedStart = computed(() => instance.value?.actual === 'error' && cloud.value?.operations[0]?.action === 'start' && cloud.value?.operations[0]?.state === 'failed')
const operationFailure = computed(() => {
  const code = instance.value?.error || (cloud.value?.operations[0]?.state === 'failed' ? cloud.value.operations[0].error : '')
  if (!code) return ''
  if (code === 'billing_insufficient_balance') return '所有者账户余额不足，团队 Cloud 未开通且未扣款。'
  if (code === 'capacity_unavailable') return '暂时没有支持团队协作的可用节点，未扣款，请稍后重试。'
  return '最近一次团队 Cloud 操作未完成，请刷新查看状态。'
})
const cloudStatus = computed(() => {
  const v = instance.value
  if (!v) return '尚未开通'
  if (v.purge_locked) return '正在删除'
  if (!paid.value) return '已到期，等待续费或回收'
  if (v.desired === 'stopped' && v.actual === 'stopped') return '已停机'
  if (canOpen.value) return '运行中'
  return v.actual === 'error' ? '需要恢复' : '正在准备，请稍候'
})
const dialogTitle = computed(() => {
  if (!pending.value) return ''
  if (pending.value.action === 'cancel') return '取消团队 Cloud 自动续费？'
  if (pending.value.action === 'keep') return '开启团队 Cloud 自动续费？'
  return renewable.value ? '启动团队 Cloud？' : '确认购买团队 Cloud？'
})
const dialogBody = computed(() => {
  if (!pending.value) return ''
  if (pending.value.action === 'cancel') return `仅关闭到期自动续费，团队 Cloud 继续运行至 ${formatDate(instance.value?.paid_until ?? null)}，到期停机后按所购套餐保留期回收删除。`
  if (pending.value.action === 'keep') return '将在当前已付费期限结束时自动从所有者账户余额续期，本次不扣款。'
  if (renewable.value) return '当前已付费期限内启动不重复收费。'
  const plan = chosenPlan.value
  return plan ? `将使用当前所有者账号余额按月预付 ${plan.monthly_price} / 月开通「${plan.name}」并开启自动续费。到期停机后保留 ${plan.retention_days} 天，超时永久删除团队实例与数据。` : '确认后从所有者余额扣款续费并启动团队 Cloud。'
})

async function loadTeamDetail(teamId: string) {
  if (!teamId) { cloud.value = null; members.value = []; return }
  const [detail, list] = await Promise.all([teamsAPI.cloud(teamId), teamsAPI.members(teamId)])
  if (alive && selectedId.value === teamId) { cloud.value = detail; members.value = list }
}

async function loadAll() {
  if (loading.value) return
  loading.value = true
  try {
    const snapshot = await teamsAPI.workspaces()
    if (!alive) return
    enabled.value = Array.isArray(snapshot?.capabilities) && snapshot.capabilities.includes('identity')
    if (!enabled.value) return
    teams.value = snapshot.workspaces ?? []
    if (!teams.value.some(item => item.id === selectedId.value)) selectedId.value = teams.value[0]?.id ?? ''
    plans.value = await teamsAPI.plans().catch(() => [])
    if (!plans.value.some(p => p.id === selectedPlanId.value)) selectedPlanId.value = plans.value[0]?.id ?? ''
    if (selectedId.value) await loadTeamDetail(selectedId.value)
    error.value = ''
  } catch {
    if (alive && enabled.value) error.value = '加载团队信息失败，请刷新重试。'
  } finally {
    if (alive) loading.value = false
  }
}

watch(selectedId, id => { lastInvite.value = null; if (id && enabled.value) void loadTeamDetail(id).catch(e => { if (alive) error.value = describe(e) }) })

async function createTeam() {
  if (!newTeamName.value || busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  if (!createKey || createKey.name !== newTeamName.value) createKey = { name: newTeamName.value, key: crypto.randomUUID() }
  try {
    const created = await teamsAPI.create(newTeamName.value, createKey.key)
    createKey = null; newTeamName.value = ''; selectedId.value = created.id
    notice.value = `已创建团队「${created.name}」。`
    await loadAll()
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function acceptInvite() {
  if (!inviteToken.value || busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const joined = await teamsAPI.accept(inviteToken.value)
    inviteToken.value = ''; selectedId.value = joined.id
    notice.value = `已加入团队「${joined.name}」。`
    await loadAll()
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

function confirmCloud(action: CloudAction) {
  error.value = ''; notice.value = ''
  pending.value = { action, key: crypto.randomUUID(), planId: !instance.value && action === 'start' ? selectedPlanId.value : undefined }
}

async function submitCloud() {
  if (!pending.value || !selectedTeam.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await teamsAPI.mutateCloud(selectedTeam.value.id, pending.value.action, pending.value.key, pending.value.planId)
    if (!alive) return
    pending.value = null
    notice.value = result.operation.state === 'failed' ? '' : '团队 Cloud 操作已受理，状态将自动更新。'
    await Promise.all([loadTeamDetail(selectedTeam.value.id), auth.refreshUser().catch(() => {})])
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function openBrowser() {
  if (!selectedTeam.value || !instance.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await teamsAPI.launchBrowser(selectedTeam.value.id)
    const url = new URL(result.url)
    if ((url.protocol !== 'https:' && !(import.meta.env.DEV && url.protocol === 'http:' && url.hostname.endsWith('.cloud.localhost'))) || !url.hostname.startsWith(instance.value.instance_id + '.') || url.pathname !== '/__cloud/launch' || url.search || !url.hash.startsWith('#code=')) throw new Error('团队网页连接地址无效。')
    if (alive) window.location.assign(url.href)
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function openDesktop() {
  if (!selectedTeam.value || !instance.value || busy.value) return
  busy.value = true; error.value = ''
  try {
    const result = await teamsAPI.launchDesktop(selectedTeam.value.id)
    const url = new URL(result.url), address = new URL(url.searchParams.get('address') || '')
    if (url.protocol !== 'donebot:' || url.hostname !== 'pair' || !address.hostname.startsWith(instance.value.instance_id + '.') || address.protocol !== 'https:' || address.pathname !== '/' || !/^omb_pair_[A-Za-z0-9_-]{43}$/.test(url.searchParams.get('token') || '')) throw new Error('客户端连接地址无效。')
    if (alive) window.location.assign(url.href)
  } catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function sendInvite() {
  if (!selectedTeam.value || !inviteEmail.value || busy.value) return
  busy.value = true; error.value = ''
  try { lastInvite.value = await teamsAPI.invite(selectedTeam.value.id, inviteEmail.value, 'member'); inviteEmail.value = '' }
  catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function revokeInvite() {
  if (!selectedTeam.value || !lastInvite.value || busy.value) return
  busy.value = true; error.value = ''
  try { await teamsAPI.revokeInvitation(selectedTeam.value.id, lastInvite.value.id); lastInvite.value = null }
  catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function changeRole(member: TeamMember) {
  if (!selectedTeam.value || busy.value) return
  busy.value = true; error.value = ''
  try { await teamsAPI.setMemberRole(selectedTeam.value.id, member.user_id, member.role === 'admin' ? 'member' : 'admin'); await loadTeamDetail(selectedTeam.value.id) }
  catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

async function removeMember(member: TeamMember) {
  if (!selectedTeam.value || busy.value) return
  if (!window.confirm(`移除 ${member.email}？后续团队访问将立即停止。`)) return
  busy.value = true; error.value = ''
  try { await teamsAPI.removeMember(selectedTeam.value.id, member.user_id); await loadTeamDetail(selectedTeam.value.id) }
  catch (e) { if (alive) error.value = describe(e) }
  finally { if (alive) busy.value = false }
}

onMounted(() => {
  void loadAll()
  timer = setInterval(() => { if (enabled.value && selectedId.value && !busy.value && !pending.value) void loadTeamDetail(selectedId.value).catch(() => {}) }, 5000)
})
onUnmounted(() => { alive = false; clearInterval(timer) })
</script>
