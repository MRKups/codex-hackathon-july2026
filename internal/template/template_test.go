package template

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryCreateLoadListAndUpdate(t *testing.T) {
	repository := newRepository(t)
	created, err := repository.Create(CreateInput{
		ID:        "increment",
		Name:      "Increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Digest == "" {
		t.Fatal("Create() returned an empty canonical digest")
	}

	loaded, err := repository.Load("increment")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != created {
		t.Fatalf("Load() = %#v, want %#v", loaded, created)
	}

	updated, err := repository.Update("increment", UpdateInput{
		Name:      "Increment safely",
		Spec:      "Return the input integer increased by one without panicking.",
		Signature: "func Increment(value int) int",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != created.ID || updated.Digest == created.Digest {
		t.Fatalf("Update() = %#v, want same ID and new digest", updated)
	}

	second, err := repository.Create(CreateInput{
		ID:        "echo",
		Name:      "Echo",
		Spec:      "Return the string unchanged.",
		Signature: "func Echo(value string) string",
	})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	templates, err := repository.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(templates) != 2 || templates[0].ID != second.ID || templates[1].ID != updated.ID {
		t.Fatalf("List() = %#v, want sorted templates", templates)
	}

	contents, err := os.ReadFile(filepath.Join(repository.Root(), "increment", templateFileName))
	if err != nil {
		t.Fatalf("read template.json: %v", err)
	}
	text := string(contents)
	if !strings.Contains(text, `"version":"v1"`) || strings.Contains(text, "digest") || strings.Contains(text, "testCode") {
		t.Fatalf("template.json = %s, want only versioned source-free task content", text)
	}
}

func TestRepositoryRejectsInvalidAndUnsafeInputs(t *testing.T) {
	repository := newRepository(t)

	for _, id := range []string{"", "../escape", "Uppercase", "9starts", "has_underscore"} {
		t.Run("id="+strings.ReplaceAll(id, "/", "-"), func(t *testing.T) {
			_, err := repository.Create(CreateInput{
				ID:        id,
				Name:      "Name",
				Spec:      "Return one.",
				Signature: "func One() int",
			})
			if err == nil {
				t.Fatal("Create() error = nil, want invalid ID rejection")
			}
		})
	}

	for _, input := range []CreateInput{
		{ID: "missing-name", Spec: "Return one.", Signature: "func One() int"},
		{ID: "missing-spec", Name: "Name", Signature: "func One() int"},
		{ID: "missing-signature", Name: "Name", Spec: "Return one."},
		{ID: "bad-signature", Name: "Name", Spec: "Return one.", Signature: "func One(value Missing) int"},
	} {
		if _, err := repository.Create(input); err == nil {
			t.Fatalf("Create(%#v) error = nil, want validation failure", input)
		}
	}

	if _, err := repository.Load("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := repository.Update("missing", UpdateInput{Name: "Name", Spec: "Return one.", Signature: "func One() int"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryRejectsSymlinkDirectoriesAndFiles(t *testing.T) {
	repository := newRepository(t)
	if err := os.MkdirAll(repository.Root(), 0o750); err != nil {
		t.Fatalf("create root: %v", err)
	}

	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(repository.Root(), "escaped")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := repository.List(); !errors.Is(err, ErrUnsafeStorage) {
		t.Fatalf("List() symlink error = %v, want ErrUnsafeStorage", err)
	}

	if err := os.Remove(filepath.Join(repository.Root(), "escaped")); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(repository.Root(), "increment"), 0o750); err != nil {
		t.Fatalf("create template directory: %v", err)
	}
	if err := os.Symlink(filepath.Join(target, "template.json"), filepath.Join(repository.Root(), "increment", templateFileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := repository.Load("increment"); !errors.Is(err, ErrUnsafeStorage) {
		t.Fatalf("Load() symlink file error = %v, want ErrUnsafeStorage", err)
	}
}

func TestRepositoryRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	target := t.TempDir()
	root := filepath.Join(parent, "templates")
	if err := os.Symlink(target, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	repository, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := repository.List(); !errors.Is(err, ErrUnsafeStorage) {
		t.Fatalf("List() root symlink error = %v, want ErrUnsafeStorage", err)
	}
}

func TestRepositoryRejectsMalformedOversizedAndMismatchedStoredTemplates(t *testing.T) {
	repository := newRepository(t)
	directory := filepath.Join(repository.Root(), "increment")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	path := filepath.Join(directory, templateFileName)

	for _, contents := range []string{
		`{"version":"v1","id":"increment","name":"Name","spec":"Return one.","signature":"func One() int","testCode":"forbidden"}`,
		`{"version":"v1","id":"different","name":"Name","spec":"Return one.","signature":"func One() int"}`,
		`not JSON`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		if _, err := repository.Load("increment"); err == nil {
			t.Fatalf("Load() error = nil for stored contents %q", contents)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxFileBytes+1)), 0o600); err != nil {
		t.Fatalf("write oversized fixture: %v", err)
	}
	if _, err := repository.Load("increment"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load() oversized error = %v, want size rejection", err)
	}
}

func TestRepositoryUpdateAtomicallyReplacesStoredDocument(t *testing.T) {
	repository := newRepository(t)
	if _, err := repository.Create(CreateInput{
		ID:        "increment",
		Name:      "Increment",
		Spec:      "Return the input plus one.",
		Signature: "func Increment(value int) int",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path := filepath.Join(repository.Root(), "increment", templateFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before update: %v", err)
	}
	if _, err := repository.Update("increment", UpdateInput{
		Name:      "Increment twice",
		Spec:      "Return the input plus two.",
		Signature: "func Increment(value int) int",
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if string(before) == string(after) || !strings.Contains(string(after), "plus two") {
		t.Fatalf("atomic update did not replace template contents: before=%s after=%s", before, after)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read template directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != templateFileName {
		t.Fatalf("template directory entries = %#v, want only template.json", entries)
	}
}

func newRepository(t *testing.T) *Repository {
	t.Helper()
	repository, err := New(Config{Root: filepath.Join(t.TempDir(), "templates")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return repository
}
