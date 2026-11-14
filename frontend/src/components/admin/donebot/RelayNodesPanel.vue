<template>
  <section class="space-y-5" aria-labelledby="relay-title">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div><h2 id="relay-title" class="text-lg font-semibold">手机中继节点</h2><p class="mt-1 text-sm text-gray-500">网关统一下发节点地址。最多启用 4 个节点；手机与桌面自动寻找可用节点，无需手动选择。</p></div>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">刷新</button>
    </header>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="settings && !settings.signing_configured" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">网关尚未配置中继签名密钥。可以先登记节点，配置签名后再启动中继。</p>
    <div v-if="settings" class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead><tr class="border-b border-gray-200 text-gray-500 dark:border-dark-700"><th class="p-3">节点</th><th class="p-3">状态</th><th class="p-3">手机会话 / 上限</th><th class="p-3">操作</th></tr></thead>
        <tbody><tr v-for="node in settings.nodes" :key="node.id" class="border-b border-gray-100 dark:border-dark-700">
          <td class="p-3"><p class="font-medium">{{ node.name }} <span class="text-xs text-gray-400">{{ node.id }}</span></p><p class="mt-1 break-all text-xs text-gray-500">{{ node.endpoint }}</p></td>
          <td class="p-3"><span :class="live(node) ? 'text-green-600' : 'text-gray-500'">{{ !node.enabled ? '已停用' : live(node) ? '在线' : '离线' }}</span><p class="mt-1 text-xs text-gray-400">{{ node.last_seen ? new Date(node.last_seen).toLocaleString() : '尚未上报' }}</p></td>
          <td class="p-3"><p>{{ live(node) ? node.sessions : '—' }} / {{ node.max_sessions }}</p><p v-if="live(node)" class="mt-1 text-xs text-gray-400">{{ node.hosts }} 台主机 · {{ node.sockets }} 个 WebSocket</p></td>
          <td class="space-y-2 p-3"><button type="button" class="btn btn-secondary" @click="edit(node)">编辑</button><button type="button" class="block text-xs text-primary-600" :disabled="saving" @click="rotate(node)">重置节点凭据</button></td>
        </tr></tbody>
      </table>
      <p v-if="!settings.nodes.length" class="py-6 text-center text-sm text-gray-500">尚未登记节点。下方默认使用 donebot.cn，无需新增域名。</p>
    </div>
    <form class="space-y-4 rounded-xl bg-gray-50 p-4 dark:bg-dark-800" @submit.prevent="save">
      <div class="flex items-center justify-between"><h3 class="font-medium">{{ editing ? '编辑节点' : '添加节点' }}</h3><button v-if="editing" type="button" class="text-sm text-primary-600" @click="reset">添加新节点</button></div>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="text-sm">节点编号<input v-model="form.id" :disabled="editing" required pattern="relay[1-9][0-9]?" class="input mt-1" placeholder="relay1" /></label>
        <label class="text-sm">名称<input v-model="form.name" required maxlength="120" class="input mt-1" /></label>
        <label class="text-sm sm:col-span-2">连接地址<input v-model="form.endpoint" required class="input mt-1" placeholder="donebot.cn/mobile-relay" /><span class="mt-1 block text-xs text-gray-500">填写 donebot.cn/mobile-relay，或独立节点域名 relay1.donebot.cn；不填写协议和端口。节点编号只是内部名称。</span></label>
        <label class="text-sm">最大手机会话数<input v-model.number="form.max_sessions" type="number" required min="1" max="100000" class="input mt-1" /></label>
        <label class="flex items-center gap-2 self-end py-3 text-sm"><input v-model="form.enabled" type="checkbox" />启用此节点</label>
      </div>
      <p class="text-xs leading-relaxed text-gray-500">一个会话指一部手机与一台电脑之间的加密连接，建立中的连接也计入。每台在线电脑有 1 条控制连接，每个会话另需 2 条 WebSocket 数据连接；例如 1 台电脑、2 个会话通常共 5 条 WebSocket。只需设置会话上限，内部保护上限由程序计算。下调上限不主动断开已有会话；停用后不再分配新连接，停用全部节点会使远程访问停止。配置约 10 秒内生效。</p>
      <button type="submit" class="btn btn-primary" :disabled="saving">{{ saving ? '正在保存…' : '保存节点' }}</button><span v-if="saved" role="status" class="ml-3 text-sm text-green-600">已保存，等待节点同步</span>
    </form>
    <details v-if="settings?.token_keys" class="text-sm">
      <summary class="cursor-pointer text-gray-500">中继验签公钥</summary>
      <textarea :value="settings.token_keys" readonly rows="3" spellcheck="false" class="input mt-2 w-full font-mono text-xs" aria-label="中继验签公钥" />
    </details>
    <section v-if="credential" class="space-y-3 rounded-xl border border-primary-200 p-4" aria-label="节点部署凭据">
      <div class="flex justify-between gap-4"><h3 class="font-medium">节点部署配置</h3><button type="button" class="text-sm text-primary-600" @click="credential = ''">关闭</button></div>
      <p class="text-sm text-gray-500">节点凭据仅本次显示，请保存到服务器 relay.env。保存配置后需要重启对应中继容器。</p>
      <textarea :value="credential" readonly rows="5" spellcheck="false" class="input w-full font-mono text-xs" aria-label="relay.env 配置" />
    </section>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { relayAPI, type RelayCredential, type RelayNode, type RelayNodeInput, type RelayNodes } from '@/api/admin/relay'
const stepUp = useStepUp()
const defaults = (): RelayNodeInput => ({ id: 'relay1', name: '主中继', endpoint: 'donebot.cn/mobile-relay', enabled: true, max_sessions: 1000 })
const form = reactive(defaults())
const settings = ref<RelayNodes | null>(null)
const editing = ref(false), loading = ref(false), saving = ref(false), saved = ref(false)
const error = ref(''), credential = ref('')
const now = ref(Date.now())
let active = true, generation = 0
let timer: ReturnType<typeof setInterval> | undefined
function live(node: RelayNode) { return !error.value && !!node.last_seen && now.value - Date.parse(node.last_seen) < 45_000 }
async function load() {
  if (loading.value) return
  loading.value = true
  try { const next = await relayAPI.list(); if (active) { settings.value = next; error.value = ''; now.value = Date.now() } }
  catch (e) { if (active) error.value = e instanceof Error ? e.message : '加载节点失败' }
  finally { if (active) loading.value = false }
}
function reset() { ++generation; editing.value = false; saved.value = false; credential.value = ''; Object.assign(form, defaults()) }
function edit(node: RelayNode) { ++generation; editing.value = true; saved.value = false; credential.value = ''; Object.assign(form, { id: node.id, name: node.name, endpoint: node.endpoint, enabled: node.enabled, max_sessions: node.max_sessions }) }
function showCredential(id: string, value: RelayCredential) { if (value.token) credential.value = `RELAY_NODE=${id}\nRELAY_NODE_TOKEN=${value.token}\nRELAY_TOKEN_KEYS=${value.token_keys || '请先配置网关签名密钥，再复制后台公钥'}\n` }
async function save() {
  if (saving.value) return
  const current = generation, input = { ...form }
  saving.value = true; saved.value = false; error.value = ''
  try { const result = await stepUp.run(() => relayAPI.save(input)); if (active && current === generation) { editing.value = true; saved.value = true; showCredential(input.id, result) } await load() }
  catch (e) { if (active && !isStepUpCancelled(e)) error.value = e instanceof Error ? e.message : '保存节点失败' }
  finally { if (active) saving.value = false }
}
async function rotate(node: RelayNode) {
  if (saving.value || !window.confirm('重置后旧节点凭据立即失效，需要更新服务器配置。继续重置？')) return
  saving.value = true
  try { const result = await stepUp.run(() => relayAPI.rotate(node.id)); if (active) showCredential(node.id, result); await load() }
  catch (e) { if (active && !isStepUpCancelled(e)) error.value = e instanceof Error ? e.message : '重置凭据失败' }
  finally { if (active) saving.value = false }
}
onMounted(() => { void load(); timer = setInterval(() => { now.value = Date.now(); if (!document.hidden) void load() }, 15_000) })
onUnmounted(() => { active = false; stepUp.onCancel(); credential.value = ''; clearInterval(timer) })
</script>
