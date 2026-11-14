import type { RouteLocationNormalized, RouteRecordRaw } from 'vue-router'
import { cloudPages } from '@/views/admin/cloudPages'

export const cloudAdminRoutes: RouteRecordRaw[] = [
  { path: '/admin/cloud', redirect: '/admin/cloud/overview', meta: { requiresAuth: true, requiresAdmin: true } },
  ...cloudPages.map(page => ({
    path: `/admin/cloud/${page.section}`,
    name: `AdminCloud-${page.section}`,
    component: () => import('@/views/admin/CloudView.vue'),
    props: { section: page.section },
    meta: { requiresAuth: true, requiresAdmin: true, title: `Cloud 管理 · ${page.label}` }
  }))
]

export function redirectLegacyCloud(to: RouteLocationNormalized) {
  return to.query.tab === 'cloud' ? { path: '/admin/cloud/overview', replace: true } : true
}
