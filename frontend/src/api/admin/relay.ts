import { apiClient } from '../client'
export interface RelayNodeInput { id: string; name: string; endpoint: string; enabled: boolean; max_sessions: number }
export interface RelayNode extends RelayNodeInput { max_hosts: number; max_sockets: number; hosts: number; sessions: number; sockets: number; last_seen: string | null }
export interface RelayNodes { nodes: RelayNode[]; token_keys: string; signing_configured: boolean }
export interface RelayCredential { token: string; token_keys: string }
export const relayAPI = {
  async list(): Promise<RelayNodes> { return (await apiClient.get<RelayNodes>('/admin/donebot/relay/nodes')).data },
  async save(input: RelayNodeInput): Promise<RelayCredential> { return (await apiClient.put<RelayCredential>(`/admin/donebot/relay/nodes/${encodeURIComponent(input.id)}`, input)).data },
  async rotate(id: string): Promise<RelayCredential> { return (await apiClient.post<RelayCredential>(`/admin/donebot/relay/nodes/${encodeURIComponent(id)}/credential`)).data }
}
