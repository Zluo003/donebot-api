import type { AxiosProgressEvent } from 'axios'
import { apiClient } from '../client'
export interface CodingGroup { engine: "codex" | "claude"; id: number; name: string; platform: string; rate_multiplier: number; status: string }
export type Modality = 'llm' | 'images' | 'audio' | 'video'
export interface ModelToggle { model: string; enabled: boolean }
export interface ModelSalePricing {
 rate_multiplier?: number | null
 input_price?: number | null; output_price?: number | null; cache_write_price?: number | null; cache_read_price?: number | null
 image_prices?: Record<string, number | null>; tier_prices?: Record<string, number | null>
 video_prices?: { resolution: string; reference_video: boolean; price_per_second: number | null }[]
}
export interface DoneBotModel extends ModelToggle {
 platform: string; available: boolean; kind: Modality
 pricing_configured?: boolean
 editable_pricing?: ModelSalePricing
 video_billing?: 'actual' | 'submitted'
 video_resolutions?: string[]
 image_input_billing?: boolean
 image_resolutions?: string[]
 channel_pricing?: { input_price: number | null; output_price: number | null; per_request_price: number | null }
 effective_input_price: number | null; effective_output_price: number | null
 effective_per_request_price?: number | null
 effective_tier_prices?: Record<string, number | null>
 audio_price_unit?: 'request' | 'million_characters' | 'million_tokens'
 effective_image_prices?: Record<string, number | null>
}
export interface ModelsResult {
 models: DoneBotModel[]; rate_multiplier: number
 image_price_1k: number | null; image_price_2k: number | null; image_price_4k: number | null
 image_rate_multiplier: number; image_rate_independent: boolean; image_count_billing: boolean
}
export interface PricingInput {
 models?: { model: string; pricing: ModelSalePricing }[]
 rate_multiplier?: number; image_price_1k?: number; image_price_2k?: number; image_price_4k?: number
 clear_image_prices?: string[]; image_rate_multiplier?: number; image_rate_independent?: boolean; image_count_billing?: boolean
}
export interface ModelConfigurationInput { models: (ModelToggle & { pricing?: ModelSalePricing })[] }
export interface AdminImageTask { task_id: string; status: string; http_status?: number; user_id: number; install: { id: number; name: string }; created_at: number; completed_at?: number; expires_at: number }
export interface Install { id: number; name: string; user_id: number; api_key_id: number; status: string; last_seen_at?: string; created_at: string }
export interface Overview { asset_storage?: { driver: string; public_base_url_configured: boolean }; group: { id: number; name: string; platform: string }; user_count: number; model_count: number; install_count: number; today_usage: { total_requests: number; total_actual_cost: number } }
export interface ObjectStorageSettings { endpoint: string; download_endpoint?: string; region: string; bucket: string; prefix: string; access_key_id: string; secret_access_key: string; force_path_style: boolean; public_base_url?: string; secret_configured?: boolean }
export const ossStorageDraft = (value?: ObjectStorageSettings, prefix = 'generated/'): ObjectStorageSettings => ({ download_endpoint: '', region: '', bucket: '', access_key_id: '', force_path_style: false, ...value, endpoint: value?.endpoint || 'https://oss-accelerate.aliyuncs.com', prefix: value?.prefix || prefix, public_base_url: '', secret_access_key: '' })
export interface StoragePolicyInput {
 enabled: boolean; driver: 'local' | 's3' | 'oss'; local_path: string; public_base_url: string; local_public_base_url?: string
 max_storage_bytes: number; ttl_hours: number; cleanup_threshold_percent: number; max_file_bytes: number
 s3: ObjectStorageSettings; oss?: ObjectStorageSettings
}
export interface StoragePolicy extends StoragePolicyInput { secret_configured?: boolean; usage_bytes?: number; file_count?: number }
export interface StorageSettingsInput { upload: StoragePolicyInput; generated: StoragePolicyInput }
export interface StorageSettings { upload: StoragePolicy; generated: StoragePolicy }
export interface ClientReleaseStorageSettings { driver: 'local' | 's3' | 'oss'; local_path: string; s3: StoragePolicyInput['s3']; oss?: ObjectStorageSettings; secret_configured?: boolean }
export type ClientReleasePlatform = 'windows-x64' | 'macos-arm64' | 'macos-arm64-dmg'
export type MobileReleasePlatform = 'android-apk'
export interface ClientReleaseArtifact { filename: string; size_bytes: number; sha256: string; sha512: string; download_url: string }
export interface ClientRelease { version: string; notes?: string; status: 'draft' | 'published' | 'deleting'; created_at: string; published_at?: string; platforms: Partial<Record<ClientReleasePlatform, ClientReleaseArtifact>> }
export interface MobileRelease { version: string; version_code: number; notes?: string; force_update?: boolean; status: 'draft' | 'published' | 'deleting'; created_at: string; published_at?: string; platforms: Partial<Record<MobileReleasePlatform, ClientReleaseArtifact>> }
export interface WebToolsSettings { enabled: boolean; key_configured: boolean }
export interface WebToolsSettingsInput { enabled: boolean; api_key?: string; clear_api_key?: boolean }
export interface CloudRuntimePolicy { outage_grace_minutes: number }
export interface TeamResources { cpu_millis: number; memory_mib: number; storage_gib: number; pids: number }
export interface TeamCloudPlan { id: string; name: string; enabled: boolean; monthly_price: string; retention_days: number; runtime: TeamResources; desktop: TeamResources; shared_quota_bytes: number; max_file_bytes: number }
const donebot = {
 async teamPlans() { return (await apiClient.get<TeamCloudPlan[]>('/admin/donebot/teams/plans')).data },
 async saveTeamPlans(input: TeamCloudPlan[]) { return (await apiClient.put<TeamCloudPlan[]>('/admin/donebot/teams/plans', input)).data },
 async cloudRuntimePolicy() { return (await apiClient.get<CloudRuntimePolicy>('/admin/donebot/cloud/runtime-policy')).data },
 async saveCloudRuntimePolicy(input: CloudRuntimePolicy) { return (await apiClient.put<CloudRuntimePolicy>('/admin/donebot/cloud/runtime-policy', input)).data },
 async cloudPlan() { return (await apiClient.get<{ enabled: boolean; monthly_price: string; retention_days: number }>('/admin/donebot/cloud/plan')).data },
 async saveCloudPlan(input: { enabled: boolean; monthly_price: string; retention_days: number }) { return (await apiClient.put<{ enabled: boolean; monthly_price: string; retention_days: number }>('/admin/donebot/cloud/plan', input)).data },
 async cloudNodes() { return (await apiClient.get<{ nodes: Array<{ id: string; name: string; endpoint: string; capacity: number; assigned: number; enabled: boolean; last_seen?: string }>; public_key: string }>('/admin/donebot/cloud/nodes')).data },
 async saveCloudNode(input: { name: string; endpoint: string; capacity: number; enabled: boolean }, id?: string) { return (await (id ? apiClient.put<{ node: { id: string }; token: string; public_key: string }>(`/admin/donebot/cloud/nodes/${id}`, input) : apiClient.post<{ node: { id: string }; token: string; public_key: string }>('/admin/donebot/cloud/nodes', input))).data },
 async deleteCloudNode(id: string) { await apiClient.delete(`/admin/donebot/cloud/nodes/${id}`) },
 async cloudInstances() { return (await apiClient.get<Array<{ id: string; user_id: number; node_id: string; desired: string; actual: string; error: string; paid_until?: string; purge_locked: boolean }>>('/admin/donebot/cloud/instances')).data },
 async changeCloudNodeCredential(id: string, revoke: boolean) { return (await apiClient.post<{ token: string; public_key: string }>(`/admin/donebot/cloud/nodes/${id}/credential`, { revoke })).data },
 async cloudEvents() { return (await apiClient.get<Array<{ instance_id: string; user_id: number; cycle: number; event: string; amount: string; balance_after: string; created_at: string }>>('/admin/donebot/cloud/events')).data },
 async webTools() { return (await apiClient.get<WebToolsSettings>('/admin/donebot/web-tools')).data },
 async saveWebTools(input: WebToolsSettingsInput) { return (await apiClient.put<WebToolsSettings>('/admin/donebot/web-tools', input)).data },
 async storage() { return (await apiClient.get<StorageSettings>('/admin/donebot/storage')).data },
 async saveStorage(input: StorageSettingsInput) { return (await apiClient.put<StorageSettings>('/admin/donebot/storage', input)).data },
 async testStorage(purpose: 'upload' | 'generated', input: StoragePolicyInput) { return (await apiClient.post<{ ok: boolean; message: string }>(`/admin/donebot/storage/${purpose}/test`, input)).data },
 async clientReleaseStorage() { return (await apiClient.get<ClientReleaseStorageSettings>('/admin/donebot/releases/storage')).data },
 async saveClientReleaseStorage(input: ClientReleaseStorageSettings) { return (await apiClient.put<ClientReleaseStorageSettings>('/admin/donebot/releases/storage', input)).data },
 async testClientReleaseStorage(input: ClientReleaseStorageSettings) { return (await apiClient.post<{ ok: boolean; message: string }>('/admin/donebot/releases/storage/test', input)).data },
 async clientReleases() { return (await apiClient.get<{ releases: ClientRelease[] }>('/admin/donebot/releases')).data },
 async uploadClientRelease(input: FormData, onUploadProgress?: (event: AxiosProgressEvent) => void) { return (await apiClient.post<ClientRelease>('/admin/donebot/releases/upload', input, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 3_600_000, onUploadProgress })).data },
 async publishClientRelease(version: string) { return (await apiClient.post<ClientRelease>(`/admin/donebot/releases/${encodeURIComponent(version)}/publish`)).data },
 async deleteClientRelease(version: string) { await apiClient.delete(`/admin/donebot/releases/${encodeURIComponent(version)}`) },
 async mobileReleases() { return (await apiClient.get<{ releases: MobileRelease[] }>('/admin/donebot/mobile-releases')).data },
 async uploadMobileRelease(input: FormData, onUploadProgress?: (event: AxiosProgressEvent) => void) { return (await apiClient.post<MobileRelease>('/admin/donebot/mobile-releases/upload', input, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 3_600_000, onUploadProgress })).data },
 async publishMobileRelease(version: string) { return (await apiClient.post<MobileRelease>(`/admin/donebot/mobile-releases/${encodeURIComponent(version)}/publish`)).data },
 async deleteMobileRelease(version: string) { await apiClient.delete(`/admin/donebot/mobile-releases/${encodeURIComponent(version)}`) },
 async codingGroups() { return (await apiClient.get<CodingGroup[]>('/admin/donebot/coding')).data },
 async overview() { return (await apiClient.get<Overview>('/admin/donebot/overview')).data },
 async getModels(kind: Modality) { return (await apiClient.get<ModelsResult>(`/admin/donebot/${kind}/models`)).data },
 async models(kind: Modality, models: ModelToggle[]) { return (await apiClient.put<ModelsResult>(`/admin/donebot/${kind}/models`, { models })).data },
 async pricing(kind: Modality, input: PricingInput) { return (await apiClient.put<ModelsResult>(`/admin/donebot/${kind}/pricing`, input)).data },
 async saveConfiguration(kind: Modality, input: ModelConfigurationInput) { return (await apiClient.put<ModelsResult>(`/admin/donebot/${kind}/configuration`, input)).data },
 async sync(kind: Modality) { return (await apiClient.post<ModelsResult>(`/admin/donebot/${kind}/models/sync`)).data },
 async installs(page = 1) { return (await apiClient.get<{ items: Install[]; total: number }>('/admin/donebot/installs', { params: { page, page_size: 20 } })).data },
 async imageTask(id: string) { return (await apiClient.get<AdminImageTask>(`/admin/donebot/images/tasks/${encodeURIComponent(id)}`)).data },
 async revoke(id: number) { await apiClient.delete(`/admin/donebot/installs/${id}`) }
}
export default donebot
