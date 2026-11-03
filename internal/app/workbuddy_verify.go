package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/workbuddy"
)

type workBuddyVerificationSink struct {
	accounts    *accounts.Service
	credentials *capabilities.CredentialService
}

func newWorkBuddyVerificationSink(
	acct *accounts.Service,
	creds *capabilities.CredentialService,
) *workBuddyVerificationSink {
	return &workBuddyVerificationSink{accounts: acct, credentials: creds}
}

func (s *workBuddyVerificationSink) AcceptVerifiedAccount(
	ctx context.Context,
	account workbuddy.VerifiedAccount,
	credential []byte,
) (created bool, err error) {
	if s.accounts == nil || s.credentials == nil {
		return false, errors.New("账号或凭据服务未接线")
	}
	ref := capabilities.CredentialRef{
		Target:    account.Target,
		AccountID: account.AccountID,
		SourceID:  account.SourceID,
		Kind:      capabilities.Managed,
	}
	_, getErr := s.accounts.Get(ctx, account.Target, account.AccountID)
	existing := getErr == nil
	if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
		return false, getErr
	}

	var oldSecret string
	if existing {
		if old, err := s.credentials.Resolve(ctx, ref); err == nil {
			oldSecret = old.Secret
		}
	}

	if err := s.credentials.Persist(ctx, ref, string(credential)); err != nil {
		return false, err
	}
	discoverErr := s.accounts.Discover(ctx, account.Target, []accounts.DiscoveredAccount{{
		ID:             account.AccountID,
		DisplayName:    account.DisplayName,
		MaskedIdentity: account.MaskedIdentity,
		SourceID:       account.SourceID,
		SourceKind:     accounts.KindManaged,
	}})
	if discoverErr != nil {
		if existing && oldSecret != "" {
			if restoreErr := s.credentials.Persist(ctx, ref, oldSecret); restoreErr != nil {
				return false, fmt.Errorf("%w; 恢复旧凭据失败: %v", discoverErr, restoreErr)
			}
		} else if !existing {
			_ = s.credentials.Delete(ctx, ref)
		}
		return false, discoverErr
	}
	if err := s.accounts.EstablishManagedSource(
		ctx, account.Target, account.AccountID, account.SourceID,
	); err != nil {
		return false, err
	}
	return !existing, nil
}
