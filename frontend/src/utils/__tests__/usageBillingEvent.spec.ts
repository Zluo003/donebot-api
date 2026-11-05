import { describe, expect, it } from 'vitest'
import { formatUsageBalanceAmount, usageBillingEvent } from '../usageBillingEvent'

describe('media billing events', () => {
  it('does not invent a media task event for older usage records', () => {
    const log = { actual_cost: 0.125 }
    expect(usageBillingEvent(log)).toBeNull()
    expect(formatUsageBalanceAmount(log)).toBe('$0.125000')
  })

  it('shows partial and full refunds as credits while retaining signed stored costs', () => {
    const log = { event_type: 'refund' as const, operation_receipt_id: 'receipt', actual_cost: -0.125 }
    expect(usageBillingEvent(log)).toBe('refund')
    expect(formatUsageBalanceAmount(log, 4)).toBe('+$0.1250')
    expect(log.actual_cost).toBe(-0.125)
    expect(usageBillingEvent({ actual_cost: -0.25 })).toBe('refund')
  })
})
