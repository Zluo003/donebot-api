import type { Account } from '@/types'

export const SEEDANCE_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const
export const SEEDANCE_MODELS = [
  { id: 'doubao-seedance-2-0-260128', label: 'Seedance 2.0', resolutions: '480p / 720p / 1080p / 4K' },
  { id: 'doubao-seedance-2-0-fast-260128', label: 'Seedance 2.0 Fast', resolutions: '480p / 720p' },
  { id: 'doubao-seedance-2-0-mini-260615', label: 'Seedance 2.0 Mini', resolutions: '480p / 720p' },
  { id: 'doubao-seedance-2-5-260628', label: 'Seedance 2.5', resolutions: '480p / 720p / 1080p' }
] as const

export function isSeedanceVideoAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'>): boolean {
  const capabilities = account.credentials?.openai_capabilities
  return account.platform === 'openai' && account.type === 'apikey' &&
    Array.isArray(capabilities) && capabilities.length === 1 && capabilities[0] === 'seedance'
}
