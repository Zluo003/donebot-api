package repository

import (
	"context"

	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.PasswordResetRepository = (*userRepository)(nil)

func (r *userRepository) CompareAndSwapPassword(ctx context.Context, userID int64, email, oldHash, newHash string) (bool, error) {
	n, err := clientFromContext(ctx, r.client).User.Update().Where(
		dbuser.IDEQ(userID), dbuser.EmailEQ(email), dbuser.PasswordHashEQ(oldHash),
		dbuser.StatusEQ(service.StatusActive), dbuser.DeletedAtIsNil(),
	).SetPasswordHash(newHash).Save(ctx)
	return n == 1, err
}
