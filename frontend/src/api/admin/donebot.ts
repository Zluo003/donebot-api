import { apiClient } from '../client'
export type Modality = 'llm' | 'images'
export interface ModelToggle { model: string; enabled: boolean }
export interface DoneBotModel extends ModelToggle {
 platform: string; available: boolean; kind: Modality
 channel_pricing?: { input_price: number | null; output_price: number | null; per_request_price: number | null }
 effective_input_price: number | null; effective_output_price: number | null
 effective_image_prices?: Record<string, number | null>
}
export interface ModelsResult {
 models: DoneBotModel[]; rate_multiplier: number
 image_price_1k: number | null; image_price_2k: number | null; image_price_4k: number | null
 image_rate_multiplier: number; image_rate_independent: boolean; image_count_billing: boolean
}
export interface PricingInput {
 rate_multiplier?: number; image_price_1k?: number; image_price_2k?: number; image_price_4k?: number
 clear_image_prices?: string[]; image_rate_multiplier?: number; image_rate_independent?: boolean; image_count_billing?: boolean
}
export interface AdminImageTask { task_id: string; status: string; http_status?: number; user_id: number; install: { id: number; name: string }; created_at: number; completed_at?: number; expires_at: number }
export interface Install { id: number; name: string; user_id: number; api_key_id: number; status: string; last_seen_at?: string; created_at: string }
export interface Overview { asset_storage?: { driver: string; public_base_url_configured: boolean }; group: { id: number; name: string; platform: string }; user_count: number; model_count: number; install_count: number; today_usage: { total_requests: number; total_actual_cost: number } }
const donebot = {
 async overview() { return (await apiClient.get<Overview>('/admin/donebot/overview')).data },
 async getModels(kind: Modality) { return (await apiClient.get<ModelsResult>(`/admin/donebot/${kind}/models`)).data },
 async models(kind: Modality, models: ModelToggle[]) { return (await apiClient.put<ModelsResult>(`/admin/donebot/${kind}/models`, { models })).data },
 async pricing(kind: Modality, input: PricingInput) { return (await apiClient.put<ModelsResult>(`/admin/donebot/${kind}/pricing`, input)).data },
 async sync(kind: Modality) { return (await apiClient.post<ModelsResult>(`/admin/donebot/${kind}/models/sync`)).data },
 async installs(page = 1) { return (await apiClient.get<{ items: Install[]; total: number }>('/admin/donebot/installs', { params: { page, page_size: 20 } })).data },
 async imageTask(id: string) { return (await apiClient.get<AdminImageTask>(`/admin/donebot/images/tasks/${encodeURIComponent(id)}`)).data },
 async revoke(id: number) { await apiClient.delete(`/admin/donebot/installs/${id}`) }
}
export default donebot
