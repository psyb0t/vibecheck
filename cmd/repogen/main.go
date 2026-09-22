// Command repogen generates Vibecheck's typed GORM repositories from the
// model structs.
//
// It is driven by `go generate ./...` through
// internal/pkg/db/repositories/gen.go, never run by hand. The generated files
// are the data-access API; nothing above the db package writes SQL or names a
// column as a string.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"gorm.io/gen"
)

const (
	defaultOutPath = "."
	outFileName    = "repositories.gen.go"
)

func main() {
	outPath := flag.String(
		"out",
		defaultOutPath,
		"directory to write the generated repositories into",
	)

	flag.Parse()

	generator := gen.NewGenerator(gen.Config{
		OutPath: *outPath,
		OutFile: outFileName,
		Mode: gen.WithDefaultQuery |
			gen.WithQueryInterface |
			gen.WithoutContext,
	})

	generator.ApplyBasic(
		models.Evaluation{},
		models.EvaluationFeedback{},
		models.IdempotencyRecord{},
		models.PolicySnapshot{},
	)

	generator.Execute()

	slog.Info("generated repositories", "out", *outPath)

	os.Exit(0)
}
