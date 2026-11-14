import { apiClient } from './client'

export interface DoneBotDevice {
  id: string; name: string; platform: string; online: boolean; connection: string
  last_seen: string | null; created_at: string
}
export interface DoneBotDevices { desktop: DoneBotDevice[]; mobile: DoneBotDevice[]; relay_configured: boolean }
export async function getDoneBotDevices(): Promise<DoneBotDevices> {
  return (await apiClient.get<DoneBotDevices>('/donebot/devices')).data
}
