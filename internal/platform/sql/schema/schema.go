package schema

import (
	"embed"

	"github.com/weaveplatform/infraweave/internal/platform/sql/migrate"
)

// Name is the platform stream's name in schema_migrations.
const Name = "platform"

// files are the platform stream's migrations, NNNNN_snake_case.sql beside
// this file.
//
//go:embed *.sql
var files embed.FS

// Stream is the platform stream, which internal/app registers before every
// domain's.
func Stream() migrate.Stream { return migrate.Stream{Name: Name, Files: files} }
