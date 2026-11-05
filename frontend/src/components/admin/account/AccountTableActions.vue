<template>
  <div class="flex flex-wrap items-center gap-3">
    <slot name="before"></slot>
    <button @click="$emit('refresh')" :disabled="loading" class="btn btn-secondary">
      <Icon name="refresh" size="md" :class="[loading ? 'animate-spin' : '']" />
    </button>
    <slot name="after"></slot>
    <slot name="beforeCreate"></slot>
    <button @click="$emit('create')" class="btn btn-primary">{{ t('admin.accounts.createAccount') }}</button>
    <button @click="$emit('create-video')" class="btn btn-secondary" data-testid="create-video-account">{{ t('admin.accounts.videoAccount.createTitle') }}</button>
    <button v-for="service in APIMART_SERVICES" :key="service.kind" @click="$emit('create-apimart', service.kind)" class="btn btn-secondary" :data-testid="`create-apimart-${service.kind}-account`">{{ t(`admin.accounts.apimartAccount.services.${service.kind}.createTitle`) }}</button>
    <slot name="afterCreate"></slot>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { APIMART_SERVICES } from '@/components/account/apimartAccount'

defineProps(['loading'])
defineEmits(['refresh', 'create', 'create-video', 'create-apimart'])

const { t } = useI18n()
</script>
