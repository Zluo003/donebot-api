<template>
  <form class="max-w-xl space-y-4" @submit.prevent="save">
    <h3 class="font-semibold">Cloud 按月预付</h3>
    <p class="text-sm text-gray-500">从用户现有账户余额扣除月费，成功后开通一个月。余额不足不续期，到期停止实例并保留数据。价格调整在下次购买或续费时生效。</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <label class="flex items-center gap-2"><input v-model="plan.enabled" type="checkbox" :disabled="busy || !loaded">启用开通和自动续费</label>
    <label class="block text-sm">每月扣除余额金额（与账户余额同单位）
      <input v-model="plan.monthly_price" class="input mt-2" inputmode="decimal" aria-label="Cloud 月费" :disabled="busy || !loaded" required pattern="(0|[1-9][0-9]{0,6})(\.[0-9]{1,6})?">
    </label>
    <label class="block text-sm">到期未续费后保留数据（天）
      <input v-model.number="plan.retention_days" class="input mt-2" type="number" min="1" max="365" step="1" aria-label="Cloud 到期保留天数" :disabled="busy || !loaded" required>
    </label>
    <button class="btn btn-primary" type="submit" :disabled="busy || !loaded">保存 Cloud 月费</button>
    <p class="text-xs text-gray-500">到期立即停机；超过保留期限后自动永久删除实例和专属数据。节点离线或删除失败时保持待处理状态。调整保留天数会应用于已有过期实例。关闭收费后不再扣取新的月费，已支付账期继续有效。停止实例不等于取消订阅；尚未成功开通的订阅取消后退回本期扣款。</p>
  </form>
  <div class="mt-6 space-y-4">
    <h3 class="font-semibold">云服务器节点</h3>
    <p class="text-sm text-gray-500">每台服务器运行一个 worker。容量决定最多分配多少个用户实例；暂停分配后，已有实例继续保留在原节点。</p>
    <p v-if="nodeError" role="alert" class="text-sm text-red-600">{{ nodeError }}</p>
    <form class="grid max-w-3xl gap-3 sm:grid-cols-2" @submit.prevent="addNode">
      <label class="text-sm">服务器名称<input v-model="newNode.name" class="input mt-1" required :disabled="nodeBusy"></label>
      <label class="text-sm">私网 HTTPS 地址<input v-model="newNode.endpoint" class="input mt-1" type="url" placeholder="https://node.internal.example" required :disabled="nodeBusy"></label>
      <label class="text-sm">用户实例容量<input v-model.number="newNode.capacity" class="input mt-1" type="number" min="1" max="10000" required :disabled="nodeBusy"></label>
      <button class="btn btn-primary self-end" type="submit" :disabled="nodeBusy">添加服务器</button>
    </form>
    <div v-if="enrollment" class="space-y-2 rounded border border-amber-400 p-3 text-sm">
      <p>节点令牌仅显示这一次，请保存到该服务器的 worker 配置。</p>
      <p>节点 ID：<code>{{ enrollment.id }}</code></p>
      <label class="block">节点令牌<input class="input mt-1" readonly :value="enrollment.token" aria-label="节点令牌" autocomplete="off"></label>
      <button class="btn btn-secondary" type="button" @click="enrollment = null">已保存，隐藏令牌</button>
    </div>
    <p v-if="publicKey" class="break-all text-xs text-gray-500">主控公钥：<code>{{ publicKey }}</code></p>
    <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th>服务器</th><th>已分配 / 容量</th><th>最近心跳</th><th>分配状态</th><th>凭据</th></tr></thead><tbody><tr v-for="node in nodes" :key="node.id"><td>{{ node.name }}</td><td>{{ node.assigned }} / {{ node.capacity }}</td><td>{{ node.last_seen ? (Date.now() - Date.parse(node.last_seen) < 30000 ? '在线' : '离线') + ' · ' + new Date(node.last_seen).toLocaleString() : '尚未连接或已撤销' }}</td><td><button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="toggleNode(node)">{{ node.enabled ? '暂停分配' : '恢复分配' }}</button></td><td><button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="credentialAction = { id: node.id, name: node.name, revoke: false }">轮换令牌</button><button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="credentialAction = { id: node.id, name: node.name, revoke: true }">撤销节点</button></td></tr></tbody></table></div>
    <section v-if="credentialAction" class="space-y-3 rounded border border-amber-400 p-3" role="dialog" aria-label="节点凭据变更确认">
      <p>{{ credentialAction.revoke ? '撤销' : '轮换' }} {{ credentialAction.name }} 的令牌？旧令牌将立即失效，节点上的用户实例会在运行租约结束后停止。{{ credentialAction.revoke ? '撤销会同时暂停新分配。' : '请立即将新令牌写入该服务器配置并重启 worker。' }}</p>
      <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="credentialAction = null">返回</button>
      <button class="btn btn-primary" type="button" :disabled="nodeBusy" @click="changeCredential">确认变更</button>
    </section>
    <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="loadNodes">刷新节点与实例</button>
    <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th>用户 / 实例</th><th>节点</th><th>实际状态</th><th>付费到期</th><th>删除状态</th></tr></thead><tbody><tr v-for="instance in instances" :key="instance.id"><td>{{ instance.user_id }} / {{ instance.id }}</td><td>{{ instance.node_id }}</td><td>{{ instance.actual }} {{ instance.error }}</td><td>{{ instance.paid_until || '未付费' }}</td><td>{{ instance.purge_locked ? '正在删除数据' : instance.desired === 'deleting' ? '等待删除' : '—' }}</td></tr></tbody></table></div>
  </div>
  <div class="mt-6 overflow-x-auto">
    <h3 class="mb-3 font-semibold">最近 100 笔 Cloud 账单</h3>
    <table class="w-full text-left text-sm"><thead><tr><th>用户</th><th>实例</th><th>账期</th><th>类型</th><th>金额</th><th>余额</th></tr></thead>
      <tbody><tr v-for="event in events" :key="`${event.instance_id}-${event.cycle}-${event.event}`"><td>{{ event.user_id }}</td><td>{{ event.instance_id }}</td><td>{{ event.cycle }}</td><td>{{ event.event === 'refund' ? '退款' : '扣款' }}</td><td>{{ event.amount }}</td><td>{{ event.balance_after }}</td></tr></tbody>
    </table>
  </div>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import donebot from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
const app = useAppStore(), stepUp = useStepUp()
const plan = ref({ enabled: false, monthly_price: '0', retention_days: 7 })
const events = ref<Awaited<ReturnType<typeof donebot.cloudEvents>>>([])
const busy = ref(false), loaded = ref(false), error = ref('')
const nodes = ref<Awaited<ReturnType<typeof donebot.cloudNodes>>['nodes']>([])
const instances = ref<Awaited<ReturnType<typeof donebot.cloudInstances>>>([])
const publicKey = ref(''), nodeBusy = ref(false), nodeError = ref('')
const newNode = ref({ name: '', endpoint: '', capacity: 10, enabled: true })
const credentialAction = ref<{ id: string; name: string; revoke: boolean } | null>(null)
const enrollment = ref<{ id: string; token: string } | null>(null)
async function loadNodes() {
  nodeBusy.value = true; nodeError.value = ''
  try { const result = await donebot.cloudNodes(); nodes.value = result.nodes; publicKey.value = result.public_key; instances.value = await donebot.cloudInstances() }
  catch { nodeError.value = '节点或实例加载失败，请检查主控签名配置后重试。' } finally { nodeBusy.value = false }
}
async function addNode() {
  if (nodeBusy.value) return
  nodeBusy.value = true; nodeError.value = ''; enrollment.value = null
  try { const result = await stepUp.run(() => donebot.saveCloudNode({ ...newNode.value })); enrollment.value = { id: result.node.id, token: result.token }; await loadNodes() }
  catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = '添加服务器失败，请检查地址与容量。' } finally { nodeBusy.value = false }
}
async function changeCredential() {
  if (!credentialAction.value || nodeBusy.value) return
  const action = credentialAction.value
  nodeBusy.value = true; nodeError.value = ''; enrollment.value = null
  try {
    const result = await stepUp.run(() => donebot.changeCloudNodeCredential(action.id, action.revoke))
    if (!action.revoke) enrollment.value = { id: action.id, token: result.token }
    credentialAction.value = null; await loadNodes()
  } catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = '节点凭据变更失败，请重试。' }
  finally { nodeBusy.value = false }
}
async function toggleNode(node: typeof nodes.value[number]) {
  if (nodeBusy.value) return
  nodeBusy.value = true; nodeError.value = ''
  try { await stepUp.run(() => donebot.saveCloudNode({ name: node.name, endpoint: node.endpoint, capacity: node.capacity, enabled: !node.enabled }, node.id)); await loadNodes() }
  catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = '更新服务器失败，请重试。' } finally { nodeBusy.value = false }
}
onMounted(async () => {
  busy.value = true
  try { plan.value = await donebot.cloudPlan(); loaded.value = true; events.value = await donebot.cloudEvents(); await loadNodes() }
  catch { error.value = 'Cloud 设置或账单加载失败，请刷新重试。' } finally { busy.value = false }
})
async function save() {
  if (busy.value || !loaded.value) return
  busy.value = true; error.value = ''
  try { plan.value = await stepUp.run(() => donebot.saveCloudPlan({ ...plan.value })); app.showSuccess('Cloud 月费已保存') }
  catch (cause) { if (!isStepUpCancelled(cause)) error.value = '保存失败，请检查金额后重试。' }
  finally { busy.value = false }
}
</script>
