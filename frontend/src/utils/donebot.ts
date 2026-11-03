import type { PublicSettings } from '@/types'

export function shouldShowAPIKeyManagement(settings?: Pick<PublicSettings, 'donebot_enabled'> | null): boolean {
  return settings?.donebot_enabled !== true
}
