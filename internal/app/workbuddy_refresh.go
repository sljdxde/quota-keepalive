package app

import (
	"context"
	"errors"
	"sync"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/workbuddy"
)

type workBuddyManagedSessionCache struct {
	mu       sync.Mutex
	sessions map[string]*workbuddy.ManagedSession
}

type workBuddyManagedCredentialStore struct {
	credentials *capabilities.CredentialService
	accounts    *accounts.Service
	ref         capabilities.CredentialRef
}

func (s *workBuddyManagedCredentialStore) Load(ctx context.Context) ([]byte, error) {
	credential, err := s.credentials.Resolve(ctx, s.ref)
	if err != nil {
		return nil, err
	}
	return []byte(credential.Secret), nil
}

func (s *workBuddyManagedCredentialStore) Save(ctx context.Context, payload []byte) error {
	return s.credentials.Persist(ctx, s.ref, string(payload))
}

func (s *workBuddyManagedCredentialStore) MarkHealthy(ctx context.Context) error {
	return s.accounts.MarkSourceHealthy(
		ctx, s.ref.Target, s.ref.AccountID, s.ref.SourceID)
}

func (s *workBuddyManagedCredentialStore) MarkReauthRequired(ctx context.Context) error {
	return s.accounts.MarkSourceFailure(
		ctx, s.ref.Target, s.ref.AccountID, s.ref.SourceID)
}

// newWorkBuddyManagedSession builds the refresh-capable request path for one
// verified managed source. Imported sources are rejected before credential
// resolution, so their tokens remain read-only.
func (a *App) newWorkBuddyManagedSession(
	ctx context.Context,
	accountID, sourceID string,
) (*workbuddy.ManagedSession, error) {
	if a.accounts == nil || a.credentials == nil {
		return nil, errors.New("账号或凭据服务未接线")
	}
	account, err := getWorkBuddyAccount(a.accounts, ctx, "workbuddy-cn", accountID)
	if err != nil {
		return nil, err
	}
	var managed bool
	for _, source := range account.Sources {
		if source.ID == sourceID && source.Kind == accounts.KindManaged && !source.ReadOnly {
			managed = true
			break
		}
	}
	if !managed {
		return nil, errors.New("WorkBuddy CN 来源不存在或不是受管来源")
	}
	key := accountID + "\x00" + sourceID
	a.workBuddySessions.mu.Lock()
	defer a.workBuddySessions.mu.Unlock()
	if session := a.workBuddySessions.sessions[key]; session != nil {
		return session, nil
	}
	ref := capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: accountID,
		SourceID: sourceID, Kind: capabilities.Managed,
	}
	session := workbuddy.NewManagedSession(workbuddy.ManagedSessionConfig{
		BaseURL: workBuddyLoginBaseURL(a.cfg.BaseURLs),
		Store: &workBuddyManagedCredentialStore{
			credentials: a.credentials,
			accounts:    a.accounts,
			ref:         ref,
		},
		Now: a.now,
	})
	if a.workBuddySessions.sessions == nil {
		a.workBuddySessions.sessions = map[string]*workbuddy.ManagedSession{}
	}
	a.workBuddySessions.sessions[key] = session
	return session, nil
}
