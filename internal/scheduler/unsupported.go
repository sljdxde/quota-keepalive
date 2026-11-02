//go:build !darwin && !windows

package scheduler

import "context"

type unsupportedScheduler struct{}

// New returns an explicit unsupported scheduler for non-target platforms.
func New() Scheduler { return unsupportedScheduler{} }

func (unsupportedScheduler) Install(context.Context, Spec) error {
	return ErrUnsupported
}

func (unsupportedScheduler) Remove(context.Context, Spec) error {
	return ErrUnsupported
}

func (unsupportedScheduler) Status(context.Context, Spec) (Status, error) {
	return Status{LastError: ErrUnsupported.Error()}, nil
}
