<template>
  <BaseDialog :show="show" :title="t(account ? 'admin.accounts.apimartAccount.editTitle' : 'admin.accounts.apimartAccount.createTitle')" width="wide" @close="close">
    <form id="apimart-account-form" class="space-y-5" @submit.prevent="save">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.apimartAccount.description') }}</p>
      <div>
        <label for="apimart-account-name" class="input-label">{{ t('admin.accounts.accountName') }}</label>
        <input id="apimart-account-name" v-model="form.name" class="input" required data-testid="apimart-name" />
      </div>
      <div>
        <label for="apimart-account-notes" class="input-label">{{ t('admin.accounts.notes') }}</label>
        <textarea id="apimart-account-notes" v-model="form.notes" class="input" rows="2" />
      </div>
      <div>
        <label for="apimart-account-url" class="input-label">{{ t('admin.accounts.apimartAccount.baseUrl') }}</label>
        <input id="apimart-account-url" v-model="form.baseURL" type="url" class="input" required data-testid="apimart-base-url" />
      </div>
      <div>
        <label for="apimart-account-key" class="input-label">{{ t('admin.accounts.apimartAccount.apiKey') }}</label>
        <input id="apimart-account-key" v-model="form.apiKey" type="password" autocomplete="new-password" :required="!account" class="input" data-testid="apimart-api-key" />
        <p v-if="account" class="input-hint">{{ t('admin.accounts.apimartAccount.keepKey') }}</p>
      </div>
      <fieldset>
        <legend class="input-label">{{ t('admin.accounts.apimartAccount.models') }}</legend>
        <div class="grid gap-2 sm:grid-cols-2">
          <label v-for="model in modelOptions" :key="model.id" class="flex min-w-0 items-start gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <input v-model="form.models" type="checkbox" :value="model.id" class="mt-1 shrink-0" :data-testid="`apimart-model-${model.id}`" />
            <span class="min-w-0 text-sm">
              <span class="font-medium">{{ model.label }}</span>
              <span class="mt-1 block break-all text-xs text-gray-500 dark:text-gray-400">{{ model.id }}</span>
            </span>
          </label>
        </div>
      </fieldset>
      <p class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">{{ t('admin.accounts.apimartAccount.pricingHint') }}</p>
      <GroupSelector v-model="form.groups" :groups="groups" platform="openai" />
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label for="apimart-account-proxy" class="input-label">{{ t('admin.accounts.apimartAccount.proxy') }}</label>
          <select id="apimart-account-proxy" v-model="form.proxyID" class="input">
            <option :value="null">{{ t('admin.accounts.apimartAccount.noProxy') }}</option>
            <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option>
          </select>
        </div>
        <div>
          <label for="apimart-account-concurrency" class="input-label">{{ t('admin.accounts.apimartAccount.concurrency') }}</label>
          <input id="apimart-account-concurrency" v-model.number="form.concurrency" type="number" min="1" step="1" required class="input" />
        </div>
      </div>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
      <button type="submit" form="apimart-account-form" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account, AdminGroup, Proxy } from '@/types'
import { APIMART_MODELS, APIMART_CAPABILITIES, apimartModelCapability } from './apimartAccount'

const props = defineProps<{ show: boolean; account?: Account | null; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; created: []; updated: [account: Account] }>()
const { t } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const saving = ref(false)
const originalMapping = ref<Record<string, string>>({})
const form = reactive({ name: '', notes: '', apiKey: '', baseURL: 'https://api.apimart.ai', models: [] as string[], groups: [] as number[], proxyID: null as number | null, concurrency: 5 })
const modelOptions = computed(() => {
  const standard = new Set<string>(APIMART_MODELS.map(model => model.id))
  return [...APIMART_MODELS, ...Object.keys(originalMapping.value).filter(id => !standard.has(id)).map(id => ({ id, label: id, capability: apimartModelCapability(originalMapping.value[id]) ?? '' }))]
})
watch(() => [props.show, props.account] as const, () => {
  form.apiKey = ''
  if (!props.show) return
  const account = props.account
  const credentials = account?.credentials ?? {}
  const mapping = credentials.model_mapping
  originalMapping.value = mapping && typeof mapping === 'object' && !Array.isArray(mapping)
    ? Object.fromEntries(Object.entries(mapping).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : {}
  Object.assign(form, {
    name: account?.name ?? '', notes: account?.notes ?? '', baseURL: credentials.base_url || 'https://api.apimart.ai',
    models: account && Object.keys(originalMapping.value).length ? Object.keys(originalMapping.value) : APIMART_MODELS
      .filter(model => !account || (Array.isArray(credentials.openai_capabilities) && credentials.openai_capabilities.includes(model.capability)))
      .map(model => model.id),
    groups: [...(account?.group_ids ?? [])], proxyID: account?.proxy_id ?? null, concurrency: account?.concurrency ?? 5
  })
}, { immediate: true })

function close() {
  if (saving.value) return
  form.apiKey = ''
  emit('close')
}
async function save() {
  if (saving.value) return
  if (form.models.length === 0) { app.showError(t('admin.accounts.apimartAccount.selectModel')); return }
  if (!form.name.trim() || (!props.account && !form.apiKey.trim()) || !Number.isInteger(form.concurrency) || form.concurrency < 1) {
    app.showError(t('admin.accounts.apimartAccount.invalidFields')); return
  }
  try {
    const url = new URL(form.baseURL.trim())
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('invalid URL')
  } catch { app.showError(t('admin.accounts.apimartAccount.invalidURL')); return }
  saving.value = true
  try {
    const previousCapabilities = props.account?.credentials?.openai_capabilities
    const credentials: Record<string, unknown> = {
      ...props.account?.credentials,
      base_url: form.baseURL.trim().replace(/\/+$/, ''),
      openai_capabilities: APIMART_CAPABILITIES.filter(capability => form.models.some(id => {
        const modelCapability = apimartModelCapability(originalMapping.value[id] ?? id)
        return modelCapability ? capability === modelCapability : Array.isArray(previousCapabilities) && previousCapabilities.includes(capability)
      })),
      model_mapping: Object.fromEntries(form.models.map(id => [id, originalMapping.value[id] ?? id]))
    }
    delete credentials.api_key
    if (form.apiKey.trim()) credentials.api_key = form.apiKey.trim()
    const payload = { name: form.name.trim(), notes: form.notes.trim() || null, credentials, group_ids: [...form.groups], proxy_id: form.proxyID, concurrency: form.concurrency }
    if (props.account) {
      const updated = await stepUp.run(() => adminAPI.accounts.update(props.account!.id, payload))
      emit('updated', updated)
    } else {
      await stepUp.run(() => adminAPI.accounts.create({ ...payload, platform: 'openai', type: 'apikey' }))
      emit('created')
    }
    form.apiKey = ''
    app.showSuccess(t('admin.accounts.apimartAccount.saved'))
    emit('close')
  } catch (err) {
    if (!isStepUpCancelled(err)) app.showError(extractApiErrorMessage(err, t('admin.accounts.apimartAccount.saveFailed')))
  } finally { saving.value = false }
}
</script>
