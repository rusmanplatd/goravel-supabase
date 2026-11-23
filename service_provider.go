package supabase

import (
	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/foundation"
)

const Binding = "goravel.supabase"

var App foundation.Application

type ServiceProvider struct{}

func (receiver *ServiceProvider) Relationship() binding.Relationship {
	return binding.Relationship{
		Bindings: []string{
			Binding,
		},
		Dependencies: []string{
			binding.Config,
			binding.DB,
			binding.Log,
		},
		ProvideFor: []string{},
	}
}

func (receiver *ServiceProvider) Register(app foundation.Application) {
	App = app

	app.Singleton(Binding, func(app foundation.Application) (any, error) {
		cfg := app.MakeConfig()
		database := app.MakeDB()
		logger := app.MakeLog()

		manager := NewManager(cfg, database, logger)
		return manager.Connection("")
	})
}

func (receiver *ServiceProvider) Boot(app foundation.Application) {
	app.Publishes("github.com/rusmanplatd/goravel-supabase", map[string]string{
		"setup/config/supabase.go": app.ConfigPath("supabase.go"),
	})
}
