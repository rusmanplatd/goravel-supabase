package main

import (
	"os"

	"github.com/goravel/framework/packages"
	"github.com/goravel/framework/packages/modify"
	"github.com/goravel/framework/support/file"
	"github.com/goravel/framework/support/path"
)

const envPlaceholders = `
SUPABASE_URL=
SUPABASE_KEY=
`

func main() {
	setup := packages.Setup(os.Args)
	configContent, err := file.GetPackageContent(setup.Paths().Module().String(), "setup/config/supabase.go")
	if err != nil {
		panic(err)
	}

	serviceProvider := "&supabase.ServiceProvider{}"
	moduleImport := setup.Paths().Module().Import()

	setup.Install(
		// Register ServiceProvider in bootstrap/providers.go (or legacy config/app.go)
		modify.RegisterProvider(moduleImport, serviceProvider),

		// Publish config/supabase.go
		modify.File(path.Config("supabase.go")).Overwrite(configContent),

		// Append SUPABASE_URL and SUPABASE_KEY placeholders to .env and .env.example when present
		modify.WhenFileExists(path.Base(".env"), modify.File(path.Base(".env")).Append(envPlaceholders)),
		modify.WhenFileExists(path.Base(".env.example"), modify.File(path.Base(".env.example")).Append(envPlaceholders)),
	).Uninstall(
		modify.File(path.Config("supabase.go")).Remove(),
		modify.UnregisterProvider(moduleImport, serviceProvider),
	).Execute()
}
