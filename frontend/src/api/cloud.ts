import { apiClient } from './client'

export interface CloudInstance {
  id: string
  desired: string
  actual: string
  error: string
  paid_until: string | null
  subscription_state: string
  purge_locked: boolean
}
export interface CloudOperation { id: string; action: string; state: string; error: string }
export interface CloudMutation { instance: CloudInstance; operation: CloudOperation }
export interface CloudPlanInfo { plan: { enabled: boolean; monthly_price: string; retention_days: number }; browser_available: boolean }
export type CloudAction = 'start' | 'stop' | 'cancel'
export interface CloudResources { cpu_cores: number; memory_bytes: number; pids: number; storage: { enforced: boolean; limit_bytes: number | null; available_bytes?: number; total_bytes?: number; available_inodes?: number }; desktop?: { enabled: boolean; cpu_cores: number; memory_bytes: number; storage: { enforced: boolean; limit_bytes: number; available_bytes?: number }; idle_timeout_seconds: number }; backups?: { automatic_interval_hours: number } }
export interface CloudBackup { id: string; state: string; created_at: string; bytes: number }
export const cloudAPI = {
  async plan(): Promise<CloudPlanInfo> { return (await apiClient.get('/donebot/cloud/plan')).data },
  async instances(): Promise<CloudInstance[]> { return (await apiClient.get('/donebot/cloud/instances')).data },
  async operations(id: string): Promise<CloudOperation[]> { return (await apiClient.get(`/donebot/cloud/instances/${id}/operations`)).data },
  async mutate(id: string | undefined, action: CloudAction, key: string): Promise<CloudMutation> {
    return (await apiClient.post(id ? `/donebot/cloud/instances/${id}/${action}` : '/donebot/cloud/instances', {}, { headers: { 'Idempotency-Key': key } })).data
  },
  async launch(id: string): Promise<{ url: string; expires_at: string }> { return (await apiClient.post(`/donebot/cloud/instances/${id}/browser-launch`, {})).data },
  async desktop(id: string): Promise<{ url: string; expires_at: string }> { return (await apiClient.post(`/donebot/cloud/instances/${id}/desktop-launch`, {})).data },
  async revoke(): Promise<void> { await apiClient.delete('/donebot/cloud/browser-sessions') },
  async resources(id: string): Promise<CloudResources> { return (await apiClient.get(`/donebot/cloud/instances/${id}/proxy/api/cloud/resources`)).data },
  async backups(id: string): Promise<CloudBackup[]> { return (await apiClient.get(`/donebot/cloud/instances/${id}/proxy/api/cloud/backups`)).data },
  async backup(id: string, action: 'create' | 'restore' | 'delete', key: string, snapshot?: string): Promise<void> {
    const path = `/donebot/cloud/instances/${id}/proxy/api/cloud/backups${snapshot ? '/' + snapshot : ''}`
    if (action === 'delete') await apiClient.delete(path)
    else await apiClient.post(path + (action === 'restore' ? '/restore' : ''), { confirm: true, request_key: key }, { timeout: 305000 })
  }
}
