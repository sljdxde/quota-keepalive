package capabilities

import (
	"context"
	"errors"
)

// LoginCredentialStore persists a completed browser-login result before the
// account has passed read-only verification. It uses the same Keychain-first,
// protected-file-fallback policy as managed source credentials.
type LoginCredentialStore struct {
	keychain SecretStore
	file     SecretStore
}

// NewLoginCredentialStore assembles secure pending-login storage.
func NewLoginCredentialStore(keychain, file SecretStore) *LoginCredentialStore {
	return &LoginCredentialStore{keychain: keychain, file: file}
}

func loginSecretAccount(sessionID string) (string, error) {
	if !safeComponent(sessionID) {
		return "", ErrUnsafeNamespace
	}
	return "login-" + sessionID, nil
}

// Save stores the opaque pending-login payload. No token parsing or logging
// happens here.
func (s *LoginCredentialStore) Save(ctx context.Context, sessionID string, payload []byte) error {
	account, err := loginSecretAccount(sessionID)
	if err != nil {
		return err
	}
	secret := string(payload)
	if s.keychain != nil {
		if err := s.keychain.Set(ctx, serviceName, account, secret); err == nil {
			if s.file != nil {
				// Best effort: remove a stale fallback copy without making a
				// successful Keychain write fail if the file is already gone.
				_ = s.file.Delete(ctx, serviceName, account)
			}
			return nil
		}
	}
	if s.file == nil {
		return errors.New("凭据后端不可用")
	}
	return s.file.Set(ctx, serviceName, account, secret)
}

// Load returns the opaque pending-login payload for read-only verification.
func (s *LoginCredentialStore) Load(ctx context.Context, sessionID string) ([]byte, error) {
	account, err := loginSecretAccount(sessionID)
	if err != nil {
		return nil, err
	}
	if s.keychain != nil {
		secret, err := s.keychain.Get(ctx, serviceName, account)
		if err == nil {
			return []byte(secret), nil
		}
	}
	if s.file == nil {
		return nil, ErrSecretNotFound
	}
	secret, err := s.file.Get(ctx, serviceName, account)
	if err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

// Delete removes both possible copies so cancellation never leaves a token.
func (s *LoginCredentialStore) Delete(ctx context.Context, sessionID string) error {
	account, err := loginSecretAccount(sessionID)
	if err != nil {
		return err
	}
	var deleted bool
	var failures []error
	if s.keychain != nil {
		if err := s.keychain.Delete(ctx, serviceName, account); err == nil {
			deleted = true
		} else if !errors.Is(err, ErrSecretNotFound) {
			failures = append(failures, err)
		}
	}
	if s.file != nil {
		if err := s.file.Delete(ctx, serviceName, account); err == nil {
			deleted = true
		} else if !errors.Is(err, ErrSecretNotFound) {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if deleted {
		return nil
	}
	return ErrSecretNotFound
}
