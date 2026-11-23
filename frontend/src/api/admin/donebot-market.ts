import { apiClient } from '../client'

export interface MarketOverview {
  total_listings: number
  published_listings: number
  pending_review_count: number
  open_report_count: number
  pending_withdrawals: number
  total_installs: number
  total_attributed_cost: number
  total_creator_reward: number
  total_withdrawn: number
}

export interface MarketSettings {
  enabled: boolean
  auto_publish_on_ai_approve: boolean
  review_model: string
  review_api_base: string
  review_api_key_configured: boolean
  attribution_days: number
  freeze_days: number
  min_withdraw_amount: number
  transfer_bonus_percent: number
  rebate_llm_percent: number
  rebate_image_percent: number
  rebate_video_percent: number
  rebate_music_percent: number
  updated_at: string
}

export interface UpdateMarketSettingsInput {
  enabled?: boolean
  auto_publish_on_ai_approve?: boolean
  review_model?: string
  review_api_base?: string
  review_api_key?: string
  clear_review_api_key?: boolean
  attribution_days?: number
  freeze_days?: number
  min_withdraw_amount?: number
  transfer_bonus_percent?: number
  rebate_llm_percent?: number
  rebate_image_percent?: number
  rebate_video_percent?: number
  rebate_music_percent?: number
}

export interface MarketCategory {
  id: number
  slug: string
  name: string
  description: string
  sort_order: number
  enabled: boolean
  listing_count?: number
}

export interface MarketSkillDocument {
  name: string
  description?: string
  instructions: string
}

export interface AIReviewDimension {
  passed: boolean
  score: number
  finding: string
}

export interface AIReviewReport {
  verdict: string
  risk_score: number
  summary: string
  dimensions?: Record<string, AIReviewDimension>
  rejection_reasons?: string[]
  fix_suggestions?: string[]
  flagged_snippets?: string[]
  model?: string
  checked_at?: string
}

export interface MarketListing {
  id: string
  author_user_id: number
  author_name: string
  category_id: number
  category_name: string
  name: string
  tagline: string
  description: string
  tags: string[]
  sample_prompts: string[]
  has_soul: boolean
  skill_count: number
  skill_names: string[]
  latest_version: number
  published_version: number
  status: string
  featured: boolean
  install_count: number
  total_attributed_cost: number
  total_creator_reward: number
  report_count: number
  last_review_note: string
  published_at?: string
  created_at: string
  updated_at: string
}

export interface MarketReleaseDetail {
  id: string
  version: number
  changelog: string
  soul_text: string
  skills: MarketSkillDocument[]
  review_status: string
  ai_verdict?: string
  ai_risk_score: number
  ai_report?: AIReviewReport
  review_note?: string
  created_at: string
}

export interface MarketListingDetail extends MarketListing {
  soul_preview: string
  skills: MarketSkillDocument[]
  latest_release?: MarketReleaseDetail
}

export interface MarketReport {
  id: number
  listing_id: string
  listing_name: string
  reporter_user_id: number
  reporter_email?: string
  reason_type: string
  detail: string
  status: string
  handle_note?: string
  handled_at?: string
  created_at: string
}

export interface MarketWithdrawal {
  id: number
  creator_user_id: number
  creator_email?: string
  creator_name?: string
  amount: number
  alipay_account: string
  alipay_real_name: string
  status: 'pending' | 'paid' | 'rejected'
  payout_trade_no?: string
  admin_note?: string
  reviewed_at?: string
  created_at: string
}

const botMarketAdmin = {
  async overview() {
    return (await apiClient.get<MarketOverview>('/admin/donebot/market/overview')).data
  },
  async settings() {
    return (await apiClient.get<MarketSettings>('/admin/donebot/market/settings')).data
  },
  async saveSettings(input: UpdateMarketSettingsInput) {
    return (await apiClient.put<MarketSettings>('/admin/donebot/market/settings', input)).data
  },
  async categories() {
    return (await apiClient.get<{ categories: MarketCategory[] }>('/admin/donebot/market/categories')).data.categories
  },
  async saveCategory(input: Partial<MarketCategory> & { slug: string; name: string }) {
    if (input.id) {
      return (await apiClient.put<MarketCategory>(`/admin/donebot/market/categories/${input.id}`, input)).data
    }
    return (await apiClient.post<MarketCategory>('/admin/donebot/market/categories', input)).data
  },
  async deleteCategory(id: number) {
    await apiClient.delete(`/admin/donebot/market/categories/${id}`)
  },
  async listings(params: { status?: string; q?: string; page?: number; page_size?: number } = {}) {
    return (await apiClient.get<{ items: MarketListing[]; total: number }>('/admin/donebot/market/bots', { params })).data
  },
  async listingDetail(id: string) {
    return (await apiClient.get<MarketListingDetail>(`/admin/donebot/market/bots/${encodeURIComponent(id)}`)).data
  },
  async reviewListing(id: string, input: { decision: string; note?: string; featured?: boolean }) {
    return (await apiClient.post<MarketListingDetail>(`/admin/donebot/market/bots/${encodeURIComponent(id)}/review`, input)).data
  },
  async reports(params: { status?: string; page?: number; page_size?: number } = {}) {
    return (await apiClient.get<{ items: MarketReport[]; total: number }>('/admin/donebot/market/reports', { params })).data
  },
  async handleReport(id: number, input: { status: 'resolved' | 'dismissed'; note?: string }) {
    await apiClient.post(`/admin/donebot/market/reports/${id}/handle`, input)
  },
  async withdrawals(params: { status?: string; page?: number; page_size?: number } = {}) {
    return (await apiClient.get<{ items: MarketWithdrawal[]; total: number }>('/admin/donebot/market/withdrawals', { params })).data
  },
  async processWithdrawal(id: number, input: { decision: 'paid' | 'rejected'; payout_trade_no?: string; admin_note?: string }) {
    return (await apiClient.post<MarketWithdrawal>(`/admin/donebot/market/withdrawals/${id}/process`, input)).data
  },
}

export default botMarketAdmin
