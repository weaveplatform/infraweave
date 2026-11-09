package pg

import (
	"embed"
	"io/fs"

	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// StreamName is the configuration stream's name in schema_migrations.
const StreamName = "configuration"

// files are the configuration stream's migrations.
//
//go:embed migrations/*.sql
var files embed.FS

// Stream is the configuration domain's migration stream, which
// internal/app registers after stacks'.
func Stream() migrate.Stream {
	sub, _ := fs.Sub(files, "migrations") // a valid path, so never an error
	return migrate.Stream{Name: StreamName, Files: sub}
}
