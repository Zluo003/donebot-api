<template>
  <AppLayout>
    <div class="min-w-0 space-y-6">
      <div><h1 class="text-2xl font-bold">Cloud 管理 · {{ page.label }}</h1><p class="mt-1 text-sm text-gray-500">{{ page.description }}</p></div>
      <nav aria-label="Cloud 管理页面" class="flex flex-wrap gap-2">
        <RouterLink v-for="item in cloudPages" :key="item.section" :to="`/admin/cloud/${item.section}`" class="btn" :class="section === item.section ? 'btn-primary' : 'btn-secondary'" :aria-current="section === item.section ? 'page' : undefined">{{ item.label }}</RouterLink>
      </nav>
      <div class="card min-w-0 p-4 sm:p-6"><component :is="panels[section]" :key="section" /></div>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import CloudOverviewPanel from '@/components/admin/cloud/CloudOverviewPanel.vue'
import CloudNodesPanel from '@/components/admin/cloud/CloudNodesPanel.vue'
import CloudInstancesPanel from '@/components/admin/cloud/CloudInstancesPanel.vue'
import CloudPlanPanel from '@/components/admin/cloud/CloudPlanPanel.vue'
import CloudBillingPanel from '@/components/admin/cloud/CloudBillingPanel.vue'
import CloudRuntimePolicyPanel from '@/components/admin/cloud/CloudRuntimePolicyPanel.vue'
import { cloudPages, type CloudSection } from './cloudPages'
const props = defineProps<{ section: CloudSection }>()
const page = computed(() => cloudPages.find(p => p.section === props.section)!)
const panels = { overview: CloudOverviewPanel, nodes: CloudNodesPanel, instances: CloudInstancesPanel, plan: CloudPlanPanel, billing: CloudBillingPanel, 'runtime-policy': CloudRuntimePolicyPanel }
</script>
