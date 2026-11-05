package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type doneBotPrepaidStub struct {
	UsageBillingRepository
	p         *DoneBotMediaPrecharge
	stage     *DoneBotMediaPrepaidSettlement
	settled   *DoneBotMediaPrepaidSettlement
	settleErr error
	repo      *doneBotMediaMemoryRepository
}

func (r *doneBotPrepaidStub) PrepayDoneBotMedia(_ context.Context, p *DoneBotMediaPrecharge) error {
	r.p = p
	return nil
}
func (r *doneBotPrepaidStub) BindDoneBotMediaTask(context.Context, string, int64, string) error {
	return nil
}
func (r *doneBotPrepaidStub) LookupDoneBotMediaPrecharge(context.Context, string, int64, string) (*DoneBotMediaPrecharge, error) {
	return r.p, nil
}
func (r *doneBotPrepaidStub) StageDoneBotMediaSettlement(_ context.Context, in DoneBotMediaPrepaidSettlement) error {
	r.stage = &in
	return nil
}
func (r *doneBotPrepaidStub) SettleDoneBotMediaPrecharge(_ context.Context, in DoneBotMediaPrepaidSettlement) (*UsageBillingApplyResult, error) {
	r.settled = &in
	if r.settleErr != nil {
		return nil, r.settleErr
	}
	r.p.State = "settled"
	r.p.SettledAmount = in.ActualAmount
	if in.Failure {
		r.p.State = "refunded"
		r.p.RefundedAmount = r.p.Amount
	}
	if r.repo != nil {
		r.repo.ops[in.ReceiptID].Status = r.p.State
	}
	return &UsageBillingApplyResult{Applied: true}, nil
}

func TestDoneBotMediaPrepaidReceiptCannotConfuseDebitWithCompletion(t *testing.T) {
	svc, input, usage := doneBotMediaFixture(t)
	begun, err := svc.Begin(context.Background(), input)
	require.NoError(t, err)
	op := begun.Operation
	stub := &doneBotPrepaidStub{p: &DoneBotMediaPrecharge{ReceiptID: op.ReceiptID, UserID: op.Owner.UserID, APIKeyID: op.Owner.APIKeyID, State: "prepaid", Amount: 0.6}}
	svc.prepaid = stub
	usage.rows = []UsageLog{{UserID: op.Owner.UserID, APIKeyID: op.Owner.APIKeyID, RequestID: DoneBotMediaChargeRequestID(op.ReceiptID), ActualCost: 0.6}}
	receipt, err := svc.Receipt(context.Background(), op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "dispatching", receipt.Status)
	require.Equal(t, "prepaid", receipt.PaymentState)
	require.Nil(t, receipt.ActualCents)
	require.Equal(t, "60", receipt.PrepaidCents.String())
	require.Zero(t, usage.calls)
	_, err = svc.Complete(context.Background(), input.Owner, op.ReceiptID, DoneBotMediaCompletion{Status: "completed", TaskID: "task", HTTPStatus: 200})
	require.NoError(t, err)
	receipt, err = svc.Receipt(context.Background(), op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "uncertain", receipt.Status)
	require.Nil(t, receipt.ActualCents)
	stub.p.State = "refunded"
	stub.p.RefundedAmount = 0.6
	receipt, err = svc.Receipt(context.Background(), op.Owner.UserID, op.Owner.InstallationID, op.ReceiptID)
	require.NoError(t, err)
	require.Equal(t, "refunded", receipt.Status)
	require.Equal(t, "0", receipt.ActualCents.String())
	require.Equal(t, "60", receipt.RefundedCents.String())
}

func TestDoneBotMediaPrepaidCompletionTimeoutDoesNotRefund(t *testing.T) {
	for _, status := range []int{408, 499} {
		svc, input, _ := doneBotMediaFixture(t)
		begun, err := svc.Begin(context.Background(), input)
		require.NoError(t, err)
		stub := &doneBotPrepaidStub{p: &DoneBotMediaPrecharge{ReceiptID: begun.Operation.ReceiptID, State: "prepaid"}}
		svc.prepaid = stub
		op, err := svc.Complete(context.Background(), input.Owner, begun.Operation.ReceiptID, DoneBotMediaCompletion{Status: "failed", HTTPStatus: status})
		require.NoError(t, err)
		require.Equal(t, "uncertain", op.Status)
		require.Nil(t, stub.settled)
	}
}

func TestDoneBotMediaPrepaidActualUsesFrozenMixedRatesAndTrustedSeconds(t *testing.T) {
	snapshot := DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: 17, Rates: map[string]DoneBotMediaUnitPrice{"1K": {Actual: 0.1, Total: 0.05}, "2K": {Actual: 0.2, Total: 0.1}}}
	cost, err := doneBotPrepaidActual(snapshot, &OpenAIForwardResult{ImageCount: 3, ImageSizeBreakdown: map[string]int{"1K": 2, "2K": 1}})
	require.NoError(t, err)
	require.Equal(t, 0.4, cost.ActualCost)
	require.Equal(t, 0.2, cost.TotalCost)
	_, err = doneBotPrepaidActual(snapshot, &OpenAIForwardResult{ImageCount: 18, ImageSize: "1K"})
	require.ErrorIs(t, err, ErrDoneBotMediaReconciliation)
	snapshot = DoneBotMediaPriceSnapshot{Kind: "video", MaxUnits: 18, InputSeconds: 3, Tier: "720p", Rates: map[string]DoneBotMediaUnitPrice{"720p": {Actual: 0.1, Total: 0.05}}}
	cost, err = doneBotPrepaidActual(snapshot, &OpenAIForwardResult{VideoActualDurationSeconds: 5.5, VideoInputDurationSeconds: 99, VideoResolution: "720p"})
	require.NoError(t, err)
	require.Equal(t, 0.85, cost.ActualCost)
	_, err = doneBotPrepaidActual(snapshot, &OpenAIForwardResult{VideoActualDurationSeconds: 15.1, VideoResolution: "720p"})
	require.ErrorIs(t, err, ErrDoneBotMediaReconciliation)
}

func TestDoneBotMediaPrepaidRecordStagesDurableSettlementAndNeverOrdinaryDebit(t *testing.T) {
	id := uuid.NewString()
	stub := &doneBotPrepaidStub{p: &DoneBotMediaPrecharge{ReceiptID: id, UserID: 1, APIKeyID: 2, AccountID: 3, Model: "image-model", Kind: "image", Amount: 0.6, State: "prepaid", Snapshot: DoneBotMediaPriceSnapshot{Kind: "image", MaxUnits: 1, Rates: map[string]DoneBotMediaUnitPrice{"1K": {Actual: 0.1, Total: 0.05}, "4K": {Actual: 0.6, Total: 0.3}}}}, settleErr: errors.New("isolated transient database error")}
	svc := &OpenAIGatewayService{usageBillingRepo: stub}
	input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "upstream", Model: "image-model", ImageCount: 1, ImageOutputSizes: []string{"1024x1024"}}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2, UserID: 1, Key: "sk-donebot-isolated"}, Account: &Account{ID: 3}}
	handled, err := svc.recordDoneBotPrepaidUsage(WithDoneBotMediaReceipt(context.Background(), id), input)
	require.True(t, handled)
	require.Equal(t, stub.settleErr, err)
	require.NotNil(t, stub.stage)
	require.Equal(t, 0.1, stub.stage.ActualAmount)
	require.Equal(t, 0.0, stub.stage.Effects.BalanceCost)
	require.Equal(t, "dbm:settle:"+id, stub.stage.Effects.RequestID)
	stub.p = nil
	handled, err = svc.recordDoneBotPrepaidUsage(context.Background(), input)
	require.True(t, handled)
	require.ErrorIs(t, err, ErrDoneBotMediaPrepaidUnavailable)
	input.Result = &OpenAIForwardResult{Model: "text-model"}
	handled, err = svc.recordDoneBotPrepaidUsage(context.Background(), input)
	require.False(t, handled)
	require.NoError(t, err)
}

func TestDoneBotMediaPrepaidAdmissionFreezesConservativeNativeBounds(t *testing.T) {
	gid := int64(7)
	one, two, four, music, video := 0.045, 0.09, 0.18, 0.12, 0.15
	billing := NewBillingService(&config.Config{}, nil)
	stub := &doneBotPrepaidStub{}
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing), usageBillingRepo: stub}
	key := &APIKey{ID: 2, UserID: 1, GroupID: &gid, User: &User{ID: 1}, Group: &Group{ID: gid, Platform: PlatformOpenAI, RateMultiplier: 1, ImagePrice1K: &one, ImagePrice2K: &two, ImagePrice4K: &four, ModelPricing: []ChannelModelPricing{
		{Models: []string{"suno"}, BillingMode: BillingModePerRequest, Intervals: []PricingInterval{{TierLabel: "v6", PerRequestPrice: &music}}},
		{Models: []string{"doubao-seedance-2-0-260128", "doubao-seedance-2-5-260628"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p|reference", PerRequestPrice: &video}}},
	}}}
	account := &Account{ID: 3}
	ctx := WithDoneBotMediaReceipt(context.Background(), uuid.NewString())
	err := svc.PrepayDoneBotAPIMart(ctx, key, account, APIMartRequestInfo{Model: "seedream-5-0-flash", Tier: "2K", Units: 17, LayerDecomposition: true}, APIMartImages)
	require.NoError(t, err)
	require.Equal(t, 17.0, stub.p.Snapshot.MaxUnits)
	require.Equal(t, 1.53, stub.p.Amount)
	require.Len(t, stub.p.Snapshot.Rates, 2)
	err = svc.PrepayDoneBotAPIMart(ctx, key, account, APIMartRequestInfo{Model: "suno", Tier: "v6", Units: 2}, APIMartSuno)
	require.NoError(t, err)
	require.Equal(t, 0.24, stub.p.Amount)
	for _, tc := range []struct {
		model string
		bound float64
	}{{"doubao-seedance-2-0-260128", 18.75}, {"doubao-seedance-2-5-260628", 33.75}} {
		err = svc.PrepayDoneBotSeedance(ctx, key, account, tc.model, GrokMediaRequestInfo{Resolution: "720p", DurationSeconds: 5, HasReferenceVideo: true, ReferenceVideoDurationSeconds: 3.75})
		require.NoError(t, err)
		require.Equal(t, tc.bound, stub.p.Snapshot.MaxUnits)
		require.InDelta(t, tc.bound*video, stub.p.Amount, 1e-8)
		video = 99
		cost, err := doneBotPrepaidActual(stub.p.Snapshot, &OpenAIForwardResult{VideoResolution: "720p", VideoActualDurationSeconds: 5, VideoInputDurationSeconds: 99})
		require.NoError(t, err)
		require.InDelta(t, (5+3.75)*0.15, cost.ActualCost, 1e-8, "snapshot preserves admission price after live card changes")
		video = 0.15
	}
}
