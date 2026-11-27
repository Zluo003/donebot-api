import type { DoneBotModel, Modality, ModelSalePricing } from '@/api/admin/donebot'
import { SEEDANCE_MODELS } from '@/components/account/videoAccount'

export interface PriceField {
  key: string
  label: string
  factor: number
  value: string | number
  resolution?: string
  referenceVideo?: boolean
}
export interface ModelPriceDraft { model: string; fields: PriceField[] }
function display(value: number | null | undefined, factor = 1): string {
  return value == null ? '' : String(Number((value * factor).toPrecision(12)))
}
export function createModelPriceDraft(row: DoneBotModel, kind: Modality): ModelPriceDraft {
  const p = row.editable_pricing ?? {}
  let fields: PriceField[] = []
  if (kind === 'llm') {
    fields = [{ key: 'rate_multiplier', label: '倍率', factor: 1, value: display(p.rate_multiplier ?? 1) }]
  } else if (kind === 'images') {
    const resolutions = row.image_resolutions ?? ['1K', '2K', '4K']
    fields = resolutions.filter(size => size !== '1.5K').map(size => ({ key: `image-${size}`, label: size === '1K' && resolutions.includes('1.5K') ? '1K / 1.5K' : size, factor: 1, value: display(p.image_prices?.[size] ?? row.effective_image_prices?.[size]) }))
    if (row.image_input_billing) fields.push({ key: 'image-input', label: '输入参考图（每张）', factor: 1, value: display(p.image_prices?.input ?? row.effective_image_prices?.input) })
  } else if (kind === 'audio') {
    if (row.audio_price_unit === 'million_tokens') {
      fields = [
        { key: 'token-input', label: '输入', factor: 1e6, value: display(p.input_price ?? row.effective_input_price, 1e6) },
        { key: 'token-output', label: '输出', factor: 1e6, value: display(p.output_price ?? row.effective_output_price, 1e6) }
      ]
    } else {
      const tiers = p.tier_prices ?? row.effective_tier_prices ?? {}
      const defaults = row.audio_price_unit === 'million_characters' || row.model === 'gpt-4o-mini-tts' || 'tts' in tiers ? ['tts'] : ['v6', 'v6-wild', 'v6-mini', 'custom-model']
      fields = [...new Set([...defaults, ...Object.keys(tiers)])].map(tier => ({ key: `tier-${tier}`, label: tier, factor: 1, value: display(tiers[tier] ?? row.effective_per_request_price) }))
    }
  } else if (row.video_billing === 'actual') {
    const resolutions = ['720p']
    fields = resolutions.map(resolution => ({
      key: `video-${resolution || 'default'}-no-reference`, label: resolution || '默认档', factor: 1,
      resolution, referenceVideo: false,
      value: display(p.video_prices?.find(price => price.resolution === resolution && !price.reference_video)?.price_per_second)
    }))
  } else {
    const official = SEEDANCE_MODELS.find(model => model.id === row.model)?.resolutions.split(' / ').map(value => value.toLowerCase())
    const resolutions = row.video_resolutions ?? official ?? [...new Set((p.video_prices ?? []).map(price => price.resolution))]
    fields = resolutions.flatMap(resolution => [false, true].map(referenceVideo => ({
      key: `video-${resolution}-${referenceVideo ? 'reference' : 'no-reference'}`,
      label: referenceVideo ? '有参考视频' : '无参考视频', factor: 1, resolution, referenceVideo,
      value: display(p.video_prices?.find(price => price.resolution.toLowerCase() === resolution.toLowerCase() && price.reference_video === referenceVideo)?.price_per_second)
    })))
  }
  return { model: row.model, fields }
}
export function validPriceDraft(draft: ModelPriceDraft): boolean {
  return draft.fields.every(field => String(field.value).trim() === '' ? field.key !== 'rate_multiplier' : Number.isFinite(Number(field.value)) && Number(field.value) >= 0)
}
export function draftToSalePricing(draft: ModelPriceDraft, kind: Modality): ModelSalePricing {
  const value = (field: PriceField) => String(field.value).trim() === '' ? null : Number(field.value) / field.factor
  if (kind === 'llm') return { rate_multiplier: Number(draft.fields[0].value) }
  if (kind === 'images') return { image_prices: Object.fromEntries(draft.fields.map(field => [field.key.slice('image-'.length), value(field)])) }
  if (kind === 'audio') {
    if (draft.fields.some(field => field.key === 'token-input' || field.key === 'token-output')) {
      const inputField = draft.fields.find(field => field.key === 'token-input')
      const outputField = draft.fields.find(field => field.key === 'token-output')
      return {
        input_price: inputField ? value(inputField) : null,
        output_price: outputField ? value(outputField) : null
      }
    }
    return { tier_prices: Object.fromEntries(draft.fields.map(field => [field.label, value(field)])) }
  }
  return { video_prices: draft.fields.map(field => ({ resolution: field.resolution!, reference_video: field.referenceVideo!, price_per_second: value(field) })) }
}
