// Package template stores source-free task-authoring inputs on disk.
package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex-hackathon-july2026/internal/domain"
)

const (
	// SchemaVersion identifies the on-disk template format.
	SchemaVersion = "v1"

	maxIDBytes        = 64
	maxNameBytes      = 120
	maxSpecBytes      = 24 << 10
	maxSignatureBytes = 2 << 10
	maxFileBytes      = 32 << 10
	templateFileName  = "template.json"
)

var (
	// ErrNotFound means a valid template ID has no stored template.
	ErrNotFound = errors.New("template not found")
	// ErrAlreadyExists means a create attempted to reuse a stable template ID.
	ErrAlreadyExists = errors.New("template already exists")
	// ErrUnsafeStorage means repository contents are not safe to read or replace.
	ErrUnsafeStorage = errors.New("unsafe template storage")
)

// Config identifies the one project-owned directory containing templates.
type Config struct {
	Root string
}

// Template is a persisted authoring input. Digest is calculated from the canonical source-free
// content and is not stored in template.json.
type Template struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
	Digest    string `json:"digest"`
}

// CreateInput supplies every user-owned field for a new template.
type CreateInput struct {
	ID        string
	Name      string
	Spec      string
	Signature string
}

// UpdateInput supplies the editable fields of an existing template. Its stable ID comes from the
// lookup path, so an update cannot rename a template directory.
type UpdateInput struct {
	Name      string
	Spec      string
	Signature string
}

// Repository is a concrete, project-root-configured template store. It intentionally has no
// knowledge of runs, providers, oracle source, or verification bundles.
type Repository struct {
	root string
}

// New constructs a repository rooted at an existing or future project-owned directory.
func New(config Config) (*Repository, error) {
	root := strings.TrimSpace(config.Root)
	if root == "" {
		return nil, errors.New("template root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve template root: %w", err)
	}
	return &Repository{root: filepath.Clean(absRoot)}, nil
}

// Root returns the configured absolute repository directory for diagnostics and composition.
func (repository *Repository) Root() string {
	return repository.root
}

// List returns all valid stored templates ordered by stable ID. A missing root is an empty
// library; it is created only when a template is first saved.
func (repository *Repository) List() ([]Template, error) {
	if err := requireDirectory(repository.root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Template{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(repository.root)
	if err != nil {
		return nil, fmt.Errorf("read template root: %w", err)
	}

	templates := make([]Template, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: template entry %q is a symlink", ErrUnsafeStorage, entry.Name())
		}
		if !entry.IsDir() {
			continue
		}
		if err := validateID(entry.Name()); err != nil {
			return nil, fmt.Errorf("%w: invalid template directory %q", ErrUnsafeStorage, entry.Name())
		}
		template, err := repository.load(entry.Name())
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	sort.Slice(templates, func(left, right int) bool {
		return templates[left].ID < templates[right].ID
	})
	return templates, nil
}

// Load returns one stored template by its stable ID.
func (repository *Repository) Load(id string) (Template, error) {
	if err := validateID(id); err != nil {
		return Template{}, err
	}
	template, err := repository.load(id)
	if errors.Is(err, os.ErrNotExist) {
		return Template{}, ErrNotFound
	}
	return template, err
}

// Create validates and atomically writes a new template. Existing IDs are never overwritten.
func (repository *Repository) Create(input CreateInput) (Template, error) {
	template, content, err := makeTemplate(input.ID, input.Name, input.Spec, input.Signature)
	if err != nil {
		return Template{}, err
	}
	if err := os.MkdirAll(repository.root, 0o750); err != nil {
		return Template{}, fmt.Errorf("create template root: %w", err)
	}
	if err := requireDirectory(repository.root); err != nil {
		return Template{}, err
	}

	directory := repository.directory(template.ID)
	if err := os.Mkdir(directory, 0o750); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Template{}, fmt.Errorf("%w: %q", ErrAlreadyExists, template.ID)
		}
		return Template{}, fmt.Errorf("create template directory: %w", err)
	}
	if err := writeAtomically(directory, content); err != nil {
		_ = os.Remove(directory)
		return Template{}, err
	}
	return template, nil
}

// Update validates and atomically replaces an existing template without changing its stable ID.
func (repository *Repository) Update(id string, input UpdateInput) (Template, error) {
	if err := validateID(id); err != nil {
		return Template{}, err
	}
	template, content, err := makeTemplate(id, input.Name, input.Spec, input.Signature)
	if err != nil {
		return Template{}, err
	}
	if err := requireDirectory(repository.root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Template{}, ErrNotFound
		}
		return Template{}, err
	}
	directory := repository.directory(id)
	if err := requireDirectory(directory); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Template{}, ErrNotFound
		}
		return Template{}, err
	}
	if err := requireRegularFile(filepath.Join(directory, templateFileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Template{}, ErrNotFound
		}
		return Template{}, err
	}
	if err := writeAtomically(directory, content); err != nil {
		return Template{}, err
	}
	return template, nil
}

func (repository *Repository) load(id string) (Template, error) {
	if err := requireDirectory(repository.root); err != nil {
		return Template{}, err
	}
	directory := repository.directory(id)
	if err := requireDirectory(directory); err != nil {
		return Template{}, err
	}
	path := filepath.Join(directory, templateFileName)
	if err := requireRegularFile(path); err != nil {
		return Template{}, err
	}
	contents, err := readBounded(path)
	if err != nil {
		return Template{}, err
	}
	var stored storedTemplate
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return Template{}, fmt.Errorf("decode template %q: %w", id, err)
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return Template{}, fmt.Errorf("decode template %q: %w", id, err)
	}
	if stored.Version != SchemaVersion {
		return Template{}, fmt.Errorf("template %q has unsupported version %q", id, stored.Version)
	}
	template, _, err := makeTemplate(stored.ID, stored.Name, stored.Spec, stored.Signature)
	if err != nil {
		return Template{}, fmt.Errorf("template %q is invalid: %w", id, err)
	}
	if template.ID != id {
		return Template{}, fmt.Errorf("template %q has mismatched ID %q", id, template.ID)
	}
	return template, nil
}

func (repository *Repository) directory(id string) string {
	return filepath.Join(repository.root, id)
}

type storedTemplate struct {
	Version   string `json:"version"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	Signature string `json:"signature"`
}

func makeTemplate(id, name, spec, signature string) (Template, []byte, error) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	spec = strings.TrimSpace(spec)
	signature = strings.TrimSpace(signature)
	if err := validateID(id); err != nil {
		return Template{}, nil, err
	}
	if name == "" {
		return Template{}, nil, errors.New("template name is required")
	}
	if len(name) > maxNameBytes {
		return Template{}, nil, fmt.Errorf("template name exceeds %d bytes", maxNameBytes)
	}
	if spec == "" {
		return Template{}, nil, errors.New("task specification is required")
	}
	if len(spec) > maxSpecBytes {
		return Template{}, nil, fmt.Errorf("task specification exceeds %d bytes", maxSpecBytes)
	}
	if signature == "" {
		return Template{}, nil, errors.New("Go function signature is required")
	}
	if len(signature) > maxSignatureBytes {
		return Template{}, nil, fmt.Errorf("Go function signature exceeds %d bytes", maxSignatureBytes)
	}
	if err := domain.ValidateSignature(signature); err != nil {
		return Template{}, nil, fmt.Errorf("Go function signature is invalid: %w", err)
	}
	contents, err := json.Marshal(storedTemplate{
		Version:   SchemaVersion,
		ID:        id,
		Name:      name,
		Spec:      spec,
		Signature: signature,
	})
	if err != nil {
		return Template{}, nil, fmt.Errorf("encode template: %w", err)
	}
	digest := sha256.Sum256(contents)
	return Template{
		ID:        id,
		Name:      name,
		Spec:      spec,
		Signature: signature,
		Digest:    hex.EncodeToString(digest[:]),
	}, contents, nil
}

func validateID(id string) error {
	if id == "" {
		return errors.New("template ID is required")
	}
	if len(id) > maxIDBytes {
		return fmt.Errorf("template ID exceeds %d bytes", maxIDBytes)
	}
	for index, character := range id {
		if (character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && character == '-') {
			continue
		}
		return errors.New("template ID must start with a lowercase letter and contain only lowercase letters, digits, and hyphens")
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrUnsafeStorage, path)
	}
	return nil
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeStorage, path)
	}
	return nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open template: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	if len(contents) > maxFileBytes {
		return nil, fmt.Errorf("template file exceeds %d bytes", maxFileBytes)
	}
	return contents, nil
}

func writeAtomically(directory string, contents []byte) error {
	file, err := os.CreateTemp(directory, ".template-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary template file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary template permissions: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary template: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary template: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary template: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, templateFileName)); err != nil {
		return fmt.Errorf("replace template atomically: %w", err)
	}
	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}
