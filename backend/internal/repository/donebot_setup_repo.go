package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type doneBotSetupRepository struct {
	client *dbent.Client
}

func NewDoneBotSetupRepository(client *dbent.Client) service.DoneBotSetupRepository {
	return &doneBotSetupRepository{client: client}
}

func (r *doneBotSetupRepository) EnsureGroup(ctx context.Context, desired *service.Group, autoCreate bool) (int64, error) {
	if desired == nil {
		return 0, errors.New("DoneBot group is required")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return 0, err
	}
	client := r.client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}
	// One transaction-scoped lock protects both group creation and its settings
	// pointer. It also handles instances starting simultaneously on an empty DB.
	if err := lockDoneBotGroupBinding(ctx, client); err != nil {
		return 0, fmt.Errorf("lock DoneBot setup: %w", err)
	}
	binding := service.DoneBotGroupBinding(desired.Platform)
	if binding == "" {
		return 0, fmt.Errorf("unsupported DoneBot group platform %q", desired.Platform)
	}
	settings := NewSettingRepository(client)
	storedID, err := settings.GetValue(ctx, binding)
	if err != nil && !errors.Is(err, service.ErrSettingNotFound) {
		return 0, err
	}
	var existing *dbent.Group
	if storedID != "" {
		id, parseErr := strconv.ParseInt(storedID, 10, 64)
		if parseErr != nil || id <= 0 {
			return 0, fmt.Errorf("invalid %s setting", binding)
		}
		existing, err = client.Group.Query().Where(group.IDEQ(id), group.DeletedAtIsNil()).Only(ctx)
		if err != nil && !dbent.IsNotFound(err) {
			return 0, err
		}
	}
	if existing == nil {
		existing, err = client.Group.Query().Where(group.NameEQ(desired.Name), group.DeletedAtIsNil()).Only(ctx)
		if err != nil && !dbent.IsNotFound(err) {
			return 0, err
		}
	}
	if existing != nil {
		if existing.Platform != desired.Platform || (desired.Platform == service.PlatformComposite && existing.Status != service.StatusActive) || existing.SubscriptionType != service.SubscriptionTypeStandard {
			return 0, fmt.Errorf("DoneBot group %d must have platform=%s and subscription_type=standard (the aggregate group must be active)", existing.ID, desired.Platform)
		}
		desired.ID = existing.ID
	} else {
		if !autoCreate {
			return 0, fmt.Errorf("DoneBot group %q does not exist and donebot.auto_create_group=false", desired.Name)
		}
		if err := createGroupRecord(ctx, client, desired); err != nil {
			return 0, err
		}
		if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventGroupChanged, nil, &desired.ID, nil); err != nil {
			return 0, err
		}
	}
	if err := settings.Set(ctx, binding, strconv.FormatInt(desired.ID, 10)); err != nil {
		return 0, err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
	}
	return desired.ID, nil
}
