import { apiClient } from '../client'

export interface TemplateEntry {
  id: string; release: string; name: string; summary: string; category: string
  kind: 'bot' | 'department'; members: string[]; skills: string[]
  sha256: string; bytes: number; published: boolean; created_at: string
}
export interface TemplateDocument {
  format: string; version: number
  package: { id: string; release: string; name: string; summary: string; [key: string]: unknown }
}
export interface TemplateRelease extends TemplateEntry { document: TemplateDocument }
const path = (entry: TemplateEntry) => `/admin/donebot/templates/${encodeURIComponent(entry.id)}/${encodeURIComponent(entry.release)}`
export default {
  async list() { return (await apiClient.get<{ templates: TemplateEntry[] }>('/admin/donebot/templates')).data.templates },
  async preview(document: unknown) { return (await apiClient.post<TemplateRelease>('/admin/donebot/templates/preview', { document })).data },
  async publish(document: TemplateDocument) { return (await apiClient.post<TemplateRelease>('/admin/donebot/templates', { document })).data },
  async get(entry: TemplateEntry) { return (await apiClient.get<TemplateRelease>(path(entry))).data },
  async visibility(entry: TemplateEntry, published: boolean) { await apiClient.patch(path(entry), { published }) }
}
