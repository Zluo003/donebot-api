package service

import "context"

// UsageAlreadyRecorded consults the existing billing transaction's durable key.
// It avoids recomputing a previously settled async task under a changed tariff.
// Apply remains the atomic arbiter if another process records after this read.
func (s *OpenAIGatewayService) UsageAlreadyRecorded(ctx context.Context, requestID string, keyID int64) (bool, error) {
	if s == nil || s.usageBillingRepo == nil || requestID == "" || keyID <= 0 {
		return false, nil
	}
	if repo := s.doneBotPrepaidRepository(); repo != nil {
		p, err := repo.LookupDoneBotMediaPrecharge(ctx, DoneBotMediaReceiptID(ctx), keyID, requestID)
		if err != nil {
			return false, err
		}
		if p != nil {
			return p.State == "settled", nil
		}
	}
	lookup, ok := s.usageBillingRepo.(interface {
		HasUsageCharge(context.Context, string, int64) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return lookup.HasUsageCharge(ctx, requestID, keyID)
}
