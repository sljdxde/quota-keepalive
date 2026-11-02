package workbuddy

import (
	"errors"

	"qka/internal/capabilities"
)

func ignoreMissingPendingCredential(err error) error {
	if errors.Is(err, ErrPendingCredentialNotFound) ||
		errors.Is(err, capabilities.ErrSecretNotFound) {
		return nil
	}
	return err
}
