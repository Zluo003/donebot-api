import type { UsageLog } from '@/types'

type UsageEvent = Pick<UsageLog, 'event_type' | 'operation_receipt_id' | 'actual_cost'>

export function usageBillingEvent(row: UsageEvent): 'charge' | 'refund' | null {
  if (row.event_type === 'refund' || row.actual_cost < 0) return 'refund'
  return row.operation_receipt_id ? 'charge' : null
}

export function formatUsageBalanceAmount(row: UsageEvent, digits = 6): string {
  const cost = Number.isFinite(row.actual_cost) ? row.actual_cost : 0
  return usageBillingEvent(row) === 'refund' ? `+$${Math.abs(cost).toFixed(digits)}` : `$${cost.toFixed(digits)}`
}
