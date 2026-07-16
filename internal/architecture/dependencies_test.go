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
