<template>
  <BaseDialog :show="show" :title="t(account ? 'admin.accounts.videoAccount.editTitle' : 'admin.accounts.videoAccount.createTitle')" width="wide" @close="close">
    <form id="video-account-form" class="space-y-5" @submit.prevent="save">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.videoAccount.description') }}</p>
      <div>
        <label for="video-account-name" class="input-label">{{ t('admin.accounts.accountName') }}</label>
        <input id="video-account-name" v-model="form.name" class="input" required data-testid="video-name" />
      </div>
      <div>
        <label for="video-account-notes" class="input-label">{{ t('admin.accounts.notes') }}</label>
        <textarea id="video-account-notes" v-model="form.notes" class="input" rows="2" />
      </div>
      <div>
        <label for="video-account-url" class="input-label">{{ t('admin.accounts.videoAccount.baseUrl') }}</label>
        <input id="video-account-url" v-model="form.baseURL" type="url" class="input" required data-testid="video-base-url" />
      </div>
      <div>
        <label for="video-account-key" class="input-label">{{ t('admin.accounts.videoAccount.apiKey') }}</label>
        <input id="video-account-key" v-model="form.apiKey" type="password" autocomplete="new-password" :required="!account" class="input" data-testid="video-api-key" />
        <p v-if="account" class="input-hint">{{ t('admin.accounts.videoAccount.keepKey') }}</p>
      </div>
      <fieldset>
        <legend class="input-label">{{ t('admin.accounts.videoAccount.models') }}</legend>
        <p class="input-hint mb-3">{{ t('admin.accounts.videoAccount.mappingHint') }}</p>
        <div class="space-y-3">
          <div v-for="(mapping, index) in form.modelMappings" :key="index" class="min-w-0 space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <div class="flex items-center justify-between gap-3">
              <label class="flex items-center gap-2 text-sm"><input v-model="mapping.enabled" type="checkbox" :data-testid="`video-model-enabled-${index}`" />{{ t('admin.accounts.videoAccount.enableMapping') }}</label>
              <button type="button" class="btn btn-secondary btn-sm" :data-testid="`video-model-remove-${index}`" @click="form.modelMappings.splice(index, 1)">{{ t('common.delete') }}</button>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block min-w-0 text-sm"><span class="mb-1 block">{{ t('admin.accounts.videoAccount.downstreamModel') }}</span><input v-model="mapping.downstream" class="input" :required="mapping.enabled" :data-testid="`video-model-downstream-${index}`" /></label>
              <label class="block min-w-0 text-sm"><span class="mb-1 block">{{ t('admin.accounts.videoAccount.upstreamModel') }}</span><input v-model="mapping.upstream" class="input" :required="mapping.enabled" :data-testid="`video-model-upstream-${index}`" /></label>
            </div>
            <label class="block min-w-0 text-sm">
              <span class="mb-1 block">{{ t('admin.accounts.videoAccount.modelSpec') }}</span>
              <select v-model="mapping.spec" class="input" :required="mapping.enabled" :data-testid="`video-model-spec-${index}`">
                <option value="">{{ t('admin.accounts.videoAccount.selectModelSpec') }}</option>
                <option v-for="model in SEEDANCE_MODELS" :key="model.id" :value="model.id">{{ model.label }} · {{ model.resolutions }}</option>
              </select>
            </label>
          </div>
        </div>
        <button type="button" class="btn btn-secondary mt-3" data-testid="video-model-add" @click="addModelMapping">{{ t('admin.accounts.videoAccount.addMapping') }}</button>
      </fieldset>
      <fieldset>
        <legend class="input-label">{{ t('admin.accounts.videoAccount.resolutions') }}</legend>
        <div class="flex flex-wrap gap-4">
          <label v-for="resolution in SEEDANCE_RESOLUTIONS" :key="resolution" class="flex items-center gap-2 text-sm">
            <input v-model="form.resolutions" type="checkbox" :value="resolution" :data-testid="`video-resolution-${resolution}`" />
            {{ resolution === '4k' ? '4K' : resolution }}
          </label>
        </div>
        <p class="input-hint">{{ t('admin.accounts.videoAccount.resolutionHint') }}</p>
      </fieldset>
      <div>
        <label for="video-output-storage" class="input-label">{{ t('admin.accounts.videoAccount.outputStorage') }}</label>
        <select id="video-output-storage" v-model="form.outputStorage" class="input" data-testid="video-output-storage">
          <option value="upstream">{{ t('admin.accounts.videoAccount.outputStorageUpstream') }}</option>
          <option value="system">{{ t('admin.accounts.videoAccount.outputStorageSystem') }}</option>
        </select>
        <p class="input-hint">{{ t('admin.accounts.videoAccount.outputStorageHint') }}</p>
      </div>
      <GroupSelector v-model="form.groups" :groups="groups" platform="openai" />
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label for="video-account-proxy" class="input-label">{{ t('admin.accounts.videoAccount.proxy') }}</label>
          <select id="video-account-proxy" v-model="form.proxyID" class="input">
            <option :value="null">{{ t('admin.accounts.videoAccount.noProxy') }}</option>
            <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }}</option>
          </select>
        </div>
        <div>
          <label for="video-account-concurrency" class="input-label">{{ t('admin.accounts.videoAccount.concurrency') }}</label>
          <input id="video-account-concurrency" v-model.number="form.concurrency" type="number" min="1" step="1" required class="input" />
        </div>
      </div>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
      <button type="submit" form="video-account-form" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account, AdminGroup, Proxy } from '@/types'
import { createSeedanceModelMappings, SEEDANCE_MODELS, SEEDANCE_RESOLUTIONS, type SeedanceModelMapping } from './videoAccount'

const props = defineProps<{ show: boolean; account?: Account | null; groups: AdminGroup[]; proxies: Proxy[] }>()
const emit = defineEmits<{ close: []; created: []; updated: [account: Account] }>()
const { t } = useI18n()
const app = useAppStore()
const stepUp = useStepUp()
const saving = ref(false)
const form = reactive({ name: '', notes: '', apiKey: '', baseURL: 'https://api.newtoken.club', modelMappings: [] as SeedanceModelMapping[], resolutions: ['720p'] as string[], groups: [] as number[], proxyID: null as number | null, concurrency: 5, outputStorage: 'upstream' as 'upstream' | 'system' })
watch(() => [props.show, props.account] as const, () => {
  form.apiKey = ''
  if (!props.show) return
  const account = props.account
  const credentials = account?.credentials ?? {}
  Object.assign(form, {
    name: account?.name ?? '', notes: account?.notes ?? '', baseURL: credentials.base_url || 'https://api.newtoken.club',
    modelMappings: createSeedanceModelMappings(credentials),
    resolutions: Array.isArray(credentials.seedance_allowed_resolutions) ? [...credentials.seedance_allowed_resolutions] : ['720p'],
    outputStorage: account?.extra?.seedance_output_storage === 'system' ? 'system' : 'upstream',
    groups: [...(account?.group_ids ?? [])], proxyID: account?.proxy_id ?? null, concurrency: account?.concurrency ?? 5
  })
}, { immediate: true })

function addModelMapping() {
  form.modelMappings.push({ enabled: true, downstream: '', upstream: '', spec: SEEDANCE_MODELS[0].id })
}
function close() {
  if (saving.value) return
  form.apiKey = ''
  emit('close')
}
async function save() {
  if (saving.value) return
  if (form.resolutions.length === 0) { app.showError(t('admin.accounts.videoAccount.selectResolution')); return }
  const mappings = form.modelMappings.filter(mapping => mapping.enabled).map(mapping => ({ downstream: mapping.downstream.trim(), upstream: mapping.upstream.trim(), spec: mapping.spec }))
  if (mappings.length === 0) { app.showError(t('admin.accounts.videoAccount.selectModel')); return }
  if (mappings.some(mapping => !mapping.downstream || !mapping.upstream || /[*?]/.test(mapping.downstream + mapping.upstream))) { app.showError(t('admin.accounts.videoAccount.invalidModelMapping')); return }
  if (new Set(mappings.map(mapping => mapping.downstream)).size !== mappings.length) { app.showError(t('admin.accounts.videoAccount.duplicateModelMapping')); return }
  if (mappings.some(mapping => !SEEDANCE_MODELS.some(model => model.id === mapping.spec))) { app.showError(t('admin.accounts.videoAccount.selectModelSpec')); return }
  if (!form.name.trim() || (!props.account && !form.apiKey.trim()) || !Number.isInteger(form.concurrency) || form.concurrency < 1) {
    app.showError(t('admin.accounts.videoAccount.invalidFields')); return
  }
  try {
    const url = new URL(form.baseURL.trim())
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('invalid URL')
  } catch { app.showError(t('admin.accounts.videoAccount.invalidURL')); return }
  saving.value = true
  try {
    const credentials: Record<string, unknown> = {
      ...props.account?.credentials,
      base_url: form.baseURL.trim().replace(/\/+$/, ''), openai_capabilities: ['seedance'],
      seedance_allowed_resolutions: [...form.resolutions],
      model_mapping: Object.fromEntries(mappings.map(mapping => [mapping.downstream, mapping.upstream])),
      seedance_model_specs: Object.fromEntries(mappings.map(mapping => [mapping.downstream, mapping.spec]))
    }
    delete credentials.api_key
    if (form.apiKey.trim()) credentials.api_key = form.apiKey.trim()
    const payload = { name: form.name.trim(), notes: form.notes.trim() || null, credentials, extra: { ...props.account?.extra, seedance_output_storage: form.outputStorage }, group_ids: [...form.groups], proxy_id: form.proxyID, concurrency: form.concurrency }
    if (props.account) {
      const updated = await stepUp.run(() => adminAPI.accounts.update(props.account!.id, payload))
      emit('updated', updated)
    } else {
      await stepUp.run(() => adminAPI.accounts.create({ ...payload, platform: 'openai', type: 'apikey' }))
      emit('created')
    }
    form.apiKey = ''
    app.showSuccess(t('admin.accounts.videoAccount.saved'))
    emit('close')
  } catch (err) {
    if (!isStepUpCancelled(err)) app.showError(extractApiErrorMessage(err, t('admin.accounts.videoAccount.saveFailed')))
  } finally { saving.value = false }
}
</script>
