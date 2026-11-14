<template>
  <div class="mt-6 space-y-4">
    <h3 class="font-semibold">云服务器节点</h3>
    <p class="text-sm text-gray-500">每台服务器运行一个 worker。容量决定最多分配多少个用户实例；暂停分配后，已有实例继续保留在原节点。</p>
    <p v-if="nodeError" role="alert" class="text-sm text-red-600">{{ nodeError }}</p>
    <div v-if="publicKey" class="max-w-3xl space-y-2 rounded border p-3 text-sm">
      <label class="block">主控公钥<input class="input mt-1 font-mono" readonly :value="publicKey" aria-label="主控公钥"></label>
      <p class="text-xs text-gray-500">填入每台服务器 worker.env 的 CLOUD_MASTER_PUBLIC_KEY。所有节点使用同一主控公钥，可随时在此查看。</p>
    </div>
    <p v-else-if="nodesLoaded" role="alert" class="text-sm text-amber-700">主控签名密钥未配置或格式错误。请在网关 /etc/sub2api/environment 设置 DONEBOT_CLOUD_SIGNING_SEED 并重启网关，再刷新节点。配置完成后才能添加服务器或轮换令牌。</p>
    <form class="grid max-w-3xl gap-3 sm:grid-cols-2" @submit.prevent="addNode">
      <label class="text-sm">服务器名称<input v-model="newNode.name" class="input mt-1" required :disabled="nodeBusy"></label>
      <label class="text-sm">私网 HTTPS 地址<input v-model="newNode.endpoint" class="input mt-1" type="url" placeholder="https://node.internal.example" required :disabled="nodeBusy"></label>
      <label class="text-sm">用户实例容量<input v-model.number="newNode.capacity" class="input mt-1" type="number" min="1" max="10000" required :disabled="nodeBusy"></label>
      <button class="btn btn-primary self-end" type="submit" :disabled="nodeBusy || !publicKey">添加服务器</button>
    </form>
    <div v-if="enrollment" class="space-y-2 rounded border border-amber-400 p-3 text-sm">
      <p>节点令牌仅显示这一次，请保存到该服务器的 worker 配置。</p>
      <p>节点 ID：<code>{{ enrollment.id }}</code></p>
      <label class="block">节点令牌<input class="input mt-1" readonly :value="enrollment.token" aria-label="节点令牌" autocomplete="off"></label>
      <label class="block">主控公钥（CLOUD_MASTER_PUBLIC_KEY）<input class="input mt-1 font-mono" readonly :value="enrollment.publicKey" aria-label="节点配置主控公钥"></label>
      <button class="btn btn-secondary" type="button" @click="enrollment = null">已保存，隐藏令牌</button>
    </div>
    <div class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead><tr><th>服务器</th><th>已分配 / 容量</th><th>最近心跳</th><th>分配状态</th><th>凭据</th><th>删除</th></tr></thead>
        <tbody>
          <tr v-for="node in nodes" :key="node.id">
            <td>{{ node.name }}</td><td>{{ node.assigned }} / {{ node.capacity }}</td>
            <td>{{ node.last_seen ? (Date.now() - Date.parse(node.last_seen) < 30000 ? '在线' : '离线') + ' · ' + new Date(node.last_seen).toLocaleString() : '尚未连接或已撤销' }}</td>
            <td><button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="toggleNode(node)">{{ node.enabled ? '暂停分配' : '恢复分配' }}</button></td>
            <td>
              <button class="btn btn-secondary" type="button" :disabled="nodeBusy || !publicKey" @click="credentialAction = { id: node.id, name: node.name, revoke: false }">轮换令牌</button>
              <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="credentialAction = { id: node.id, name: node.name, revoke: true }">撤销节点</button>
            </td>
            <td>
              <button class="btn btn-secondary" type="button" :disabled="nodeBusy || node.assigned > 0" @click="deletionAction = { id: node.id, name: node.name }">删除服务器</button>
              <p v-if="node.assigned > 0" class="text-xs text-gray-500">需先回收全部实例，含停机和待删除实例。</p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <section v-if="credentialAction" class="space-y-3 rounded border border-amber-400 p-3" role="dialog" aria-label="节点凭据变更确认">
      <p>{{ credentialAction.revoke ? '撤销' : '轮换' }} {{ credentialAction.name }} 的令牌？旧令牌将立即失效，节点收到鉴权拒绝后会停止实例；失联时最迟在已有运行租约结束后停止。{{ credentialAction.revoke ? '撤销会同时暂停新分配。' : '请立即将新令牌写入该服务器配置并重启 worker。' }}</p>
      <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="credentialAction = null">返回</button>
      <button class="btn btn-primary" type="button" :disabled="nodeBusy" @click="changeCredential">确认变更</button>
    </section>
    <section v-if="deletionAction" class="space-y-3 rounded border border-amber-400 p-3" role="dialog" aria-label="删除服务器确认">
      <p>删除 {{ deletionAction.name }}？该节点将从列表移除，节点令牌立即失效。仅能删除没有实例的节点；历史实例和账单记录保留。服务器上的 worker 和 agent 仍需自行停止和卸载。</p>
      <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="deletionAction = null">返回</button>
      <button class="btn btn-primary" type="button" :disabled="nodeBusy" @click="deleteNode">确认删除</button>
    </section>
    <button class="btn btn-secondary" type="button" :disabled="nodeBusy" @click="loadNodes">刷新节点</button>
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
const nodes = ref<Awaited<ReturnType<typeof donebot.cloudNodes>>['nodes']>([])
const publicKey = ref(''), nodesLoaded = ref(false), nodeBusy = ref(false), nodeError = ref('')
const newNode = ref({ name: '', endpoint: '', capacity: 10, enabled: true })
const credentialAction = ref<{ id: string; name: string; revoke: boolean } | null>(null)
const deletionAction = ref<{ id: string; name: string } | null>(null)
const enrollment = ref<{ id: string; token: string; publicKey: string } | null>(null)
function nodeFailure(cause: unknown, fallback: string) {
  const error = cause as { code?: string; reason?: string } | null
  if ([error?.code, error?.reason].includes('CLOUD_SIGNING_UNAVAILABLE')) return '主控签名密钥未配置或格式错误，请设置 DONEBOT_CLOUD_SIGNING_SEED 并重启网关。'
  if ([error?.code, error?.reason].includes('CLOUD_NODE_IN_USE')) return '该服务器仍有实例（含停机或待删除实例），请先完成回收，再刷新节点。'
  return fallback
}
async function loadNodes() {
  nodeBusy.value = true; nodeError.value = ''
  try { const result = await donebot.cloudNodes(); nodes.value = result.nodes; publicKey.value = result.public_key; nodesLoaded.value = true }
  catch { nodeError.value = '节点加载失败，请刷新重试。' } finally { nodeBusy.value = false }
}
async function addNode() {
  if (nodeBusy.value || !publicKey.value) return
  nodeBusy.value = true; nodeError.value = ''; enrollment.value = null
  try {
    const result = await stepUp.run(() => donebot.saveCloudNode({ ...newNode.value }))
    publicKey.value = result.public_key
    enrollment.value = { id: result.node.id, token: result.token, publicKey: result.public_key }
    await loadNodes()
  } catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = nodeFailure(cause, '添加服务器失败，请检查地址与容量。') } finally { nodeBusy.value = false }
}
async function changeCredential() {
  if (!credentialAction.value || nodeBusy.value) return
  const action = credentialAction.value
  nodeBusy.value = true; nodeError.value = ''; enrollment.value = null
  try {
    const result = await stepUp.run(() => donebot.changeCloudNodeCredential(action.id, action.revoke))
    if (!action.revoke) {
      publicKey.value = result.public_key
      enrollment.value = { id: action.id, token: result.token, publicKey: result.public_key }
    }
    credentialAction.value = null; await loadNodes()
  } catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = nodeFailure(cause, '节点凭据变更失败，请重试。') }
  finally { nodeBusy.value = false }
}
async function deleteNode() {
  if (!deletionAction.value || nodeBusy.value) return
  const action = deletionAction.value
  nodeBusy.value = true; nodeError.value = ''
  try {
    await stepUp.run(() => donebot.deleteCloudNode(action.id))
    nodes.value = nodes.value.filter(node => node.id !== action.id)
    if (enrollment.value?.id === action.id) enrollment.value = null
    deletionAction.value = null
    app.showSuccess('服务器节点已删除')
    await loadNodes()
  } catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = nodeFailure(cause, '删除服务器失败，请刷新节点后重试。') }
  finally { nodeBusy.value = false }
}
async function toggleNode(node: typeof nodes.value[number]) {
  if (nodeBusy.value) return
  nodeBusy.value = true; nodeError.value = ''
  try { await stepUp.run(() => donebot.saveCloudNode({ name: node.name, endpoint: node.endpoint, capacity: node.capacity, enabled: !node.enabled }, node.id)); await loadNodes() }
  catch (cause) { if (!isStepUpCancelled(cause)) nodeError.value = '更新服务器失败，请重试。' } finally { nodeBusy.value = false }
}
onMounted(loadNodes)
</script>
