import type { Account } from '@/types'

export const SEEDANCE_RESOLUTIONS = ['480p', '720p', '1080p', '4k'] as const
export const SEEDANCE_MODELS = [
  { id: 'doubao-seedance-2-0-260128', label: 'Seedance 2.0', resolutions: '480p / 720p / 1080p / 4K' },
  { id: 'doubao-seedance-2-0-fast-260128', label: 'Seedance 2.0 Fast', resolutions: '480p / 720p' },
  { id: 'doubao-seedance-2-0-mini-260615', label: 'Seedance 2.0 Mini', resolutions: '480p / 720p' },
  { id: 'doubao-seedance-2-5-260628', label: 'Seedance 2.5', resolutions: '480p / 720p / 1080p' }
] as const

export interface SeedanceModelMapping {
  enabled: boolean
  downstream: string
  upstream: string
  spec: string
}

function stringRecord(value: unknown): Record<string, string> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? Object.fromEntries(Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : {}
}

export function createSeedanceModelMappings(credentials: Record<string, unknown> = {}): SeedanceModelMapping[] {
  const mapping = stringRecord(credentials.model_mapping)
  const specs = stringRecord(credentials.seedance_model_specs)
  const official = new Set<string>(SEEDANCE_MODELS.map(model => model.id))
  if (!Object.keys(mapping).length) return SEEDANCE_MODELS.map(model => ({ enabled: true, downstream: model.id, upstream: model.id, spec: model.id }))
  return Object.entries(mapping).map(([downstream, upstream]) => ({
    enabled: true, downstream, upstream,
    spec: specs[downstream] !== undefined ? official.has(specs[downstream]) ? specs[downstream] : '' : official.has(upstream) ? upstream : official.has(downstream) ? downstream : ''
  }))
}

export function isSeedanceVideoAccount(account: Pick<Account, 'platform' | 'type' | 'credentials'>): boolean {
  const capabilities = account.credentials?.openai_capabilities
  return account.platform === 'openai' && account.type === 'apikey' &&
    Array.isArray(capabilities) && capabilities.length === 1 && capabilities[0] === 'seedance'
}
