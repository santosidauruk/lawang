package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestApplicationAndDomainDoNotImportPostgresAdapterTypes(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{"internal/application", "internal/domain"} {
		err := filepath.WalkDir(filepath.Join(root, relative), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imported := range parsed.Imports {
				name, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if forbiddenAdapterImport(name) {
					t.Errorf("%s imports forbidden adapter dependency %q", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s imports: %v", relative, err)
		}
	}
}

func TestSessionEventApplicationQueriesAreAppendAndReadOnly(t *testing.T) {
	queryPath := filepath.Join(repositoryRoot(t), "sql/queries/session_events.sql")
	queries, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read Session Event query surface: %v", err)
	}
	upper := strings.ToUpper(string(queries))
	for _, mutation := range []string{"UPDATE SESSION_EVENTS", "DELETE FROM SESSION_EVENTS"} {
		if strings.Contains(upper, mutation) {
			t.Errorf("Session Event application query surface contains forbidden mutation %q", mutation)
		}
	}
}

func TestPersonalDetailsApplicationQueriesNeverUpdateOrDeleteDetails(t *testing.T) {
	queryPath := filepath.Join(repositoryRoot(t), "sql/queries/personal_details.sql")
	queries, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read Personal Details query surface: %v", err)
	}
	upper := strings.ToUpper(string(queries))
	for _, mutation := range []string{"UPDATE PERSONAL_DETAILS", "DELETE FROM PERSONAL_DETAILS"} {
		if strings.Contains(upper, mutation) {
			t.Errorf("Personal Details application query surface contains forbidden mutation %q", mutation)
		}
	}
}

func TestOnlyCommandPackagesComposeRuntimeComponents(t *testing.T) {
	root := repositoryRoot(t)
	compositionOnly := map[string]struct{}{
		"github.com/jackc/pgx/v5/pgxpool":                              {},
		"github.com/santosidauruk/lawang/internal/adapter/httpapi":     {},
		"github.com/santosidauruk/lawang/internal/adapter/postgres":    {},
		"github.com/santosidauruk/lawang/internal/platform/config":     {},
		"github.com/santosidauruk/lawang/internal/platform/httpserver": {},
		"github.com/santosidauruk/lawang/internal/platform/logging":    {},
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(relative, "cmd"+string(filepath.Separator)) {
			return nil
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if _, found := compositionOnly[name]; found {
				t.Errorf("%s composes runtime-only dependency %q outside cmd", relative, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect runtime composition imports: %v", err)
	}
}

func TestProviderSubmissionAPIDoesNotComposeQueueOrProviderClients(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{
		"internal/application/providersubmission/service.go",
		"internal/adapter/httpapi/router.go",
		"cmd/api/main.go",
	} {
		path := filepath.Join(root, relative)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s imports: %v", relative, err)
		}
		for _, imported := range parsed.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode %s import: %v", relative, err)
			}
			for _, forbidden := range []string{
				"github.com/hibiken/asynq",
				"github.com/redis",
				"/internal/adapter/queue",
				"/internal/adapter/provider",
			} {
				if strings.Contains(name, forbidden) {
					t.Errorf("%s imports forbidden synchronous submission dependency %q", relative, name)
				}
			}
		}
	}
}

func forbiddenAdapterImport(name string) bool {
	return strings.Contains(name, "github.com/jackc/pgx") ||
		strings.Contains(name, "/internal/adapter/postgres/sqlc") ||
		strings.Contains(name, "pgtype")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
}
