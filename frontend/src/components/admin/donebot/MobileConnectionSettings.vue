<template>
  <section class="space-y-5 border-b border-gray-200 pb-6 dark:border-dark-700" aria-labelledby="mobile-settings-title">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div><h2 id="mobile-settings-title" class="text-lg font-semibold">手机连接与消息推送</h2><p class="mt-1 text-sm text-gray-500">在此配置网关签名与 Apple 推送，保存后自动生效。</p></div>
      <button type="button" class="btn btn-secondary" :disabled="loading || saving" @click="load">刷新配置</button>
    </header>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="settings && !settings.encryption_configured" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">网关尚未设置持久加密主密钥，请先在部署配置中设置固定的 TOTP_ENCRYPTION_KEY，再保存凭据。已有部署密钥请保持不变。</p>
    <form v-if="settings" class="space-y-5" @submit.prevent="save">
      <fieldset :disabled="saving || !settings.encryption_configured" class="space-y-4 rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
        <legend class="px-1 font-medium">中继签名 <span class="ml-2 text-xs font-normal" :class="settings.relay_configured ? 'text-green-600' : 'text-gray-500'">{{ settings.relay_configured ? '已配置' : '未配置' }}</span></legend>
        <label class="block text-sm">签名 Key ID<input v-model.trim="form.relay_kid" :disabled="settings.relay_configured && relayMode === 'keep'" required pattern="k[0-9]{1,3}" maxlength="4" class="input mt-1" placeholder="k1" /></label>
        <label class="block text-sm">签名密钥<select v-model="relayMode" class="input mt-1"><option v-if="settings.relay_configured" value="keep">保留现有密钥</option><option value="generate">由网关生成新密钥</option><option value="import">导入已有 seed</option></select></label>
        <label v-if="relayMode === 'import'" class="block text-sm">Ed25519 seed<input v-model.trim="form.relay_seed" type="password" autocomplete="new-password" required pattern="[A-Za-z0-9_-]{43}" class="input mt-1 font-mono" placeholder="32 字节、无填充 base64url" /></label>
        <p v-if="settings.relay_configured && relayMode !== 'keep'" class="text-sm text-amber-700 dark:text-amber-300">轮换时请使用新的 Key ID。保存后需把下方验签公钥更新到所有独立节点，并重启节点；手机与电脑将重新连接。</p>
        <p class="text-xs text-gray-500">私钥加密保存在网关数据库中，不会回显。独立节点只使用下方的验签公钥。</p>
      </fieldset>
      <fieldset :disabled="saving || !settings.encryption_configured" class="space-y-4 rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
        <legend class="px-1 font-medium">Apple 消息推送 <span class="ml-2 text-xs font-normal" :class="settings.push_configured ? 'text-green-600' : 'text-gray-500'">{{ settings.push_configured ? '已启用' : settings.apns_key_configured ? '已配置，未启用' : '未配置' }}</span></legend>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.push_enabled" type="checkbox" />启用 iOS 新消息推送</label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-sm">Apple Key ID<input v-model.trim="form.apns_key_id" :required="form.push_enabled || !!form.apns_key || settings.apns_key_configured" pattern="[A-Z0-9]{10}" maxlength="10" class="input mt-1" placeholder="10 位 Key ID" /></label>
          <label class="text-sm">Apple Team ID<input v-model.trim="form.apns_team_id" :required="form.push_enabled || !!form.apns_key || settings.apns_key_configured" pattern="[A-Z0-9]{10}" maxlength="10" class="input mt-1" placeholder="10 位 Team ID" /></label>
        </div>
        <label class="block text-sm">APNs .p8 私钥<input ref="keyFile" type="file" accept=".p8" class="mt-2 block w-full text-sm" @change="readKey" /><span class="mt-2 block text-xs text-gray-500">{{ keySelected ? '已选择新私钥，保存后生效。' : settings.apns_key_configured ? '已保存私钥。未选择新文件时保留原私钥。' : '从 Apple Developer 获取启用 APNs 的 .p8 密钥。' }}</span></label>
        <p class="text-xs leading-relaxed text-gray-500">App 标识：cn.donebot.mobile。开发签名与正式签名分别使用 sandbox 和 production，App 会自动选择，无需上架后才能接收推送。</p>
      </fieldset>
      <div class="flex flex-wrap items-center gap-3"><button type="submit" class="btn btn-primary" :disabled="saving || !settings.encryption_configured">{{ saving ? '正在保存…' : '保存连接与推送配置' }}</button><span v-if="saved" role="status" class="text-sm text-green-600">已保存并生效</span><span class="text-xs text-gray-500">{{ settings.source === 'database' ? '当前使用后台配置' : '尚未保存后台配置；兼容读取已有部署环境' }}</span></div>
    </form>
  </section>
  <TotpStepUpDialog :controller="stepUp" />
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { mobileSettingsAPI, type MobileSettings, type MobileSettingsInput } from '@/api/admin/mobile-settings'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
const emit = defineEmits<{ saved: [] }>()
const stepUp = useStepUp()
const settings = ref<MobileSettings | null>(null)
const form = reactive<MobileSettingsInput>({ relay_kid: 'k1', relay_seed: '', push_enabled: false, apns_key_id: '', apns_team_id: '', apns_key: '' })
const relayMode = ref<'keep' | 'generate' | 'import'>('generate')
const loading = ref(false), saving = ref(false), saved = ref(false), error = ref(''), keySelected = ref(false)
const keyFile = ref<HTMLInputElement>()
let active = true, fileGeneration = 0
function clearSecrets() { ++fileGeneration; form.relay_seed = ''; form.apns_key = ''; keySelected.value = false; if (keyFile.value) keyFile.value.value = '' }
function accept(value: MobileSettings) {
  settings.value = value
  Object.assign(form, { relay_kid: value.relay_kid || 'k1', push_enabled: value.push_enabled, apns_key_id: value.apns_key_id, apns_team_id: value.apns_team_id })
  relayMode.value = value.relay_configured ? 'keep' : 'generate'
  clearSecrets()
}
async function load() {
  if (loading.value || saving.value) return
  loading.value = true; error.value = ''; saved.value = false
  try { const value = await mobileSettingsAPI.get(); if (active) accept(value) }
  catch (e) { if (active) error.value = e instanceof Error ? e.message : '加载手机配置失败' }
  finally { if (active) loading.value = false }
}
async function readKey(event: Event) {
  const generation = ++fileGeneration
  form.apns_key = ''; keySelected.value = false; saved.value = false
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    if (!file.name.toLowerCase().endsWith('.p8') || file.size > 8192) throw new Error('请选择不超过 8 KB 的 Apple APNs .p8 文件。')
    const text = await file.text()
    if (!text.includes('-----BEGIN PRIVATE KEY-----')) throw new Error('文件不是有效的 .p8 私钥。')
    if (active && generation === fileGeneration) { form.apns_key = text; keySelected.value = true; error.value = '' }
  } catch (e) { if (active && generation === fileGeneration) { error.value = e instanceof Error ? e.message : '读取私钥失败'; if (keyFile.value) keyFile.value.value = '' } }
}
async function save() {
  if (saving.value || !settings.value?.encryption_configured) return
  const replacing = settings.value.relay_configured && relayMode.value !== 'keep'
  if (replacing && form.relay_kid === settings.value.relay_kid) { error.value = '轮换签名密钥时请使用新的 Key ID。'; return }
  if (replacing && !window.confirm('轮换后需要更新所有独立节点的验签公钥并重启节点，手机与电脑将重新连接。继续保存？')) return
  if (form.push_enabled && !settings.value.apns_key_configured && !form.apns_key) { error.value = '请先选择 Apple APNs .p8 私钥。'; return }
  let input: MobileSettingsInput | undefined = { ...form, generate_relay: relayMode.value === 'generate', relay_seed: relayMode.value === 'import' ? form.relay_seed : undefined }
  clearSecrets()
  saving.value = true; saved.value = false; error.value = ''
  try { const value = await stepUp.run(() => mobileSettingsAPI.save(input!)); if (active) { accept(value); saved.value = true; emit('saved') } }
  catch (e) { if (active && !isStepUpCancelled(e)) error.value = e instanceof Error ? e.message : '保存手机配置失败' }
  finally { input = undefined; if (active) saving.value = false }
}
onMounted(() => { void load() })
onUnmounted(() => { active = false; stepUp.onCancel(); clearSecrets() })
</script>
