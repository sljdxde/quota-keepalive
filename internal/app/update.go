package app

import "qka/internal/selfupdate"

func (a *App) updateService() *selfupdate.Service {
	return selfupdate.New(a.cfg.Version, a.cfg.ConfigDir)
}
