//go:build !darwin && !windows

package selfupdate

import (
	"context"
	"errors"
	"os"
)

func startInstaller(context.Context, string, string) error {
	return errors.New("selfupdate: platform installer is unsupported")
}

func (s *Service) installer() (string, error) {
	exe := s.Executable
	if exe == nil {
		exe = os.Executable
	}
	return exe()
}
