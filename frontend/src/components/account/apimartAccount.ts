import type { Account } from '@/types'

export const APIMART_MODELS = [
  { id: 'seedream-5-0-flash', label: 'Seedream 5.0 Flash', capability: 'apimart_images' },
  { id: 'suno', label: 'Suno V6', capability: 'apimart_suno' },
  { id: 'gpt-4o-mini-tts', label: 'GPT-4o Mini TTS', capability: 'apimart_tts' }
] as const
export const APIMART_CAPABILITIES = ['apimart_images', 'apimart_suno', 'apimart_tts'] as const

export function isApimartAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'>): boolean {
  const capabilities = account.credentials?.openai_capabilities
  return account.platform === 'openai' && account.type === 'apikey' && Array.isArray(capabilities) &&
    capabilities.length > 0 && capabilities.every(value => APIMART_CAPABILITIES.some(capability => capability === value))
}

export function apimartModelCapability(model: string): string | undefined {
  if (model === 'seedream-5.0-pro') return 'apimart_images'
  return APIMART_MODELS.find(option => option.id === model)?.capability
}
