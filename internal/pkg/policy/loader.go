package policy

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/psyb0t/ctxerrors"
	"gopkg.in/yaml.v3"
)

// DefaultMaxPolicyBytes bounds one policy document. It applies to a mounted
// file and to an inline policy in a request body, because the same compiler
// consumes both.
const DefaultMaxPolicyBytes = 262144

// isPolicyExtension reports whether ext is a recognised policy file
// extension. Anything else in the policy directory is ignored rather than
// guessed at, so a stray README or .swp file is not a startup failure.
func isPolicyExtension(ext string) bool {
	switch ext {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// ParseDocument decodes one policy document with unknown-field checking.
//
// YAML is a superset of JSON, so this accepts both a mounted YAML file and an
// inline policy re-encoded as JSON. That is deliberate: named and inline
// policies must go through one parser and one compiler, or they drift.
func ParseDocument(data []byte, maxBytes int) (*Document, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxPolicyBytes
	}

	if len(data) > maxBytes {
		return nil, ctxerrors.Wrapf(
			ErrPolicyTooLarge,
			"document is %d bytes, limit is %d", len(data), maxBytes,
		)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ctxerrors.Wrap(ErrInvalidDocument, "document is empty")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	doc := &Document{}
	if err := decoder.Decode(doc); err != nil {
		return nil, ctxerrors.Wrap(ErrInvalidDocument, err.Error())
	}

	// A second successful decode means the payload carried more than one
	// YAML document or a trailing concatenated value. Both are ambiguous
	// about which policy was meant, so neither is accepted.
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ctxerrors.Wrap(
			ErrInvalidDocument, "document contains more than one YAML document",
		)
	}

	return doc, nil
}

// CompileBytes parses and compiles one document in a single step.
func CompileBytes(data []byte, maxBytes int) (*Compiled, error) {
	return CompileBytesWithMaxQuestions(data, maxBytes, MaxQuestions)
}

// CompileBytesWithMaxQuestions parses and compiles one document with a
// deployment-specific question ceiling.
func CompileBytesWithMaxQuestions(
	data []byte,
	maxBytes, maxQuestions int,
) (*Compiled, error) {
	doc, err := ParseDocument(data, maxBytes)
	if err != nil {
		return nil, err
	}

	return CompileWithMaxQuestions(doc, maxQuestions)
}

// Set is an immutable collection of compiled policies keyed by identity. It is
// built once at startup and never mutated, which is what makes "policy changes
// take effect after restart" a property of the type rather than a convention.
type Set struct {
	byRef map[Ref]*Compiled
	refs  []Ref
}

// NewSet indexes the supplied policies, rejecting a duplicate identity.
func NewSet(policies []*Compiled) (*Set, error) {
	set := &Set{byRef: make(map[Ref]*Compiled, len(policies))}

	for _, compiled := range policies {
		if _, exists := set.byRef[compiled.Ref]; exists {
			return nil, ctxerrors.Wrapf(
				ErrDuplicatePolicy,
				"%s is declared more than once", compiled.Ref,
			)
		}

		set.byRef[compiled.Ref] = compiled
		set.refs = append(set.refs, compiled.Ref)
	}

	sort.Slice(set.refs, func(i, j int) bool {
		if set.refs[i].Name != set.refs[j].Name {
			return set.refs[i].Name < set.refs[j].Name
		}

		return set.refs[i].Version < set.refs[j].Version
	})

	return set, nil
}

// Get returns the policy with this identity.
func (s *Set) Get(ref Ref) (*Compiled, bool) {
	compiled, ok := s.byRef[ref]

	return compiled, ok
}

// Len reports how many policies are loaded.
func (s *Set) Len() int {
	return len(s.refs)
}

// Refs returns every loaded identity, ordered by name then version.
func (s *Set) Refs() []Ref {
	return append([]Ref(nil), s.refs...)
}

// All returns every loaded policy in Refs order.
func (s *Set) All() []*Compiled {
	out := make([]*Compiled, 0, len(s.refs))
	for _, ref := range s.refs {
		out = append(out, s.byRef[ref])
	}

	return out
}

// LoadDir reads, parses, and compiles every policy file in dir.
//
// It refuses anything that is not a plain regular file directly inside dir: a
// symlink (even one pointing somewhere legitimate), a device, a socket, or a
// nested directory. A policy directory is mounted read-only from somewhere the
// operator controls, and following a link out of it is exactly the escape this
// check exists to prevent.
//
// An empty dir yields an empty set rather than an error, so a deployment can
// start with inline policies only.
func LoadDir(dir string, maxBytes int) (*Set, error) {
	return LoadDirWithMaxQuestions(dir, maxBytes, MaxQuestions)
}

// LoadDirWithMaxQuestions loads every policy with a deployment-specific
// question ceiling.
func LoadDirWithMaxQuestions(
	dir string,
	maxBytes, maxQuestions int,
) (*Set, error) {
	if dir == "" {
		return NewSet(nil)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ctxerrors.Wrapf(err, "read policy directory %q", dir)
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()
		if !isPolicyExtension(strings.ToLower(filepath.Ext(name))) {
			continue
		}

		names = append(names, name)
	}

	sort.Strings(names)

	policies := make([]*Compiled, 0, len(names))

	for _, name := range names {
		compiled, err := loadPolicyFile(dir, name, maxBytes, maxQuestions)
		if err != nil {
			return nil, err
		}

		policies = append(policies, compiled)
	}

	return NewSet(policies)
}

func loadPolicyFile(
	dir, name string,
	maxBytes, maxQuestions int,
) (*Compiled, error) {
	if name != filepath.Base(name) {
		return nil, ctxerrors.Wrapf(
			ErrUnsafePolicyPath, "%q is not a plain file name", name,
		)
	}

	path := filepath.Join(dir, name)

	info, err := os.Lstat(path)
	if err != nil {
		return nil, ctxerrors.Wrapf(err, "stat policy file %q", path)
	}

	if err := checkRegularFile(path, info); err != nil {
		return nil, err
	}

	if info.Size() > int64(resolveMaxBytes(maxBytes)) {
		return nil, ctxerrors.Wrapf(
			ErrPolicyTooLarge,
			"%q is %d bytes, limit is %d",
			path, info.Size(), resolveMaxBytes(maxBytes),
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ctxerrors.Wrapf(err, "read policy file %q", path)
	}

	compiled, err := CompileBytesWithMaxQuestions(
		data,
		maxBytes,
		maxQuestions,
	)
	if err != nil {
		return nil, ctxerrors.Wrapf(err, "compile policy file %q", path)
	}

	return compiled, nil
}

func checkRegularFile(path string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return ctxerrors.Wrapf(ErrUnsafePolicyPath, "%q is a symlink", path)
	}

	if !info.Mode().IsRegular() {
		return ctxerrors.Wrapf(
			ErrUnsafePolicyPath, "%q is not a regular file", path,
		)
	}

	return nil
}

func resolveMaxBytes(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultMaxPolicyBytes
	}

	return maxBytes
}
