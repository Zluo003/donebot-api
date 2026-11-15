import { apiClient } from '../client'

export interface MobileSettings {
  relay_configured: boolean
  relay_kid: string
  push_enabled: boolean
  push_configured: boolean
  apns_key_id: string
  apns_team_id: string
  apns_key_configured: boolean
  encryption_configured: boolean
  source: 'database' | 'environment'
}
export interface MobileSettingsInput {
  relay_kid: string
  relay_seed?: string
  generate_relay?: boolean
  push_enabled: boolean
  apns_key_id: string
  apns_team_id: string
  apns_key?: string
}
export const mobileSettingsAPI = {
  async get(): Promise<MobileSettings> { return (await apiClient.get<MobileSettings>('/admin/donebot/mobile/settings')).data },
  async save(input: MobileSettingsInput): Promise<MobileSettings> { return (await apiClient.put<MobileSettings>('/admin/donebot/mobile/settings', input)).data }
}
