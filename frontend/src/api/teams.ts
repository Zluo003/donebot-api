import { apiClient } from './client'
import type { CloudAction, CloudOperation } from './cloud'

export type TeamRole = 'owner' | 'admin' | 'member'
export interface TeamIdentity {
  id: string
  name: string
  role: TeamRole
  member_version: number
  created_at: string
}
export interface TeamWorkspaceSnapshot {
  protocol: string
  capabilities: string[]
  workspaces: TeamIdentity[]
}
export interface TeamMember {
  user_id: number
  email: string
  role: TeamRole
  version: number
}
export interface TeamInvitation {
  id: string
  email: string
  role: 'admin' | 'member'
  expires_at: string
  token: string
}
export interface TeamResources {
  cpu_millis: number
  memory_mib: number
  storage_gib: number
  pids: number
}
export interface TeamCloudPlan {
  id: string
  name: string
  enabled: boolean
  monthly_price: string
  retention_days: number
  runtime: TeamResources
  desktop: TeamResources
  shared_quota_bytes: number
  max_file_bytes: number
}
export interface TeamCloudState {
  instance_id: string
  desired: string
  actual: string
  generation: number
  error: string
  paid_until: string | null
  subscription_state: string
  purge_locked: boolean
  auto_renew: boolean
  plan_id?: string
  plan_name?: string
}
export interface TeamCloudDetail {
  instance: TeamCloudState | null
  operations: CloudOperation[]
  connection_available: boolean
}

export const teamsAPI = {
  async workspaces(): Promise<TeamWorkspaceSnapshot> {
    return (await apiClient.get('/donebot/workspaces')).data
  },
  async plans(): Promise<TeamCloudPlan[]> {
    return (await apiClient.get('/donebot/teams/plans')).data
  },
  async create(name: string, key: string): Promise<TeamIdentity> {
    return (await apiClient.post('/donebot/teams', { name }, { headers: { 'Idempotency-Key': key } })).data
  },
  async accept(token: string): Promise<TeamIdentity> {
    return (await apiClient.post('/donebot/teams/invitations/accept', { token })).data
  },
  async members(teamId: string): Promise<TeamMember[]> {
    return (await apiClient.get(`/donebot/teams/${teamId}/members`)).data
  },
  async invite(teamId: string, email: string, role: 'admin' | 'member' = 'member'): Promise<TeamInvitation> {
    return (await apiClient.post(`/donebot/teams/${teamId}/invitations`, { email, role })).data
  },
  async revokeInvitation(teamId: string, invitationId: string): Promise<void> {
    await apiClient.delete(`/donebot/teams/${teamId}/invitations/${invitationId}`)
  },
  async setMemberRole(teamId: string, userId: number, role: 'admin' | 'member'): Promise<void> {
    await apiClient.patch(`/donebot/teams/${teamId}/members/${userId}`, { role })
  },
  async removeMember(teamId: string, userId: number): Promise<void> {
    await apiClient.delete(`/donebot/teams/${teamId}/members/${userId}`)
  },
  async cloud(teamId: string): Promise<TeamCloudDetail> {
    return (await apiClient.get(`/donebot/teams/${teamId}/cloud`)).data
  },
  async mutateCloud(teamId: string, action: CloudAction, key: string, planId?: string): Promise<{ instance: TeamCloudState | null; operation: CloudOperation }> {
    const payload = planId ? { plan_id: planId } : {}
    return (await apiClient.post(`/donebot/teams/${teamId}/cloud/${action}`, payload, { headers: { 'Idempotency-Key': key } })).data
  },
  async launchBrowser(teamId: string): Promise<{ url: string; expires_at: string }> {
    return (await apiClient.post(`/donebot/teams/${teamId}/browser-launch`, {})).data
  },
  async launchDesktop(teamId: string): Promise<{ url: string; expires_at: string }> {
    return (await apiClient.post(`/donebot/teams/${teamId}/desktop-launch`, {})).data
  }
}
