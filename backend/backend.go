// Package backend defines the read-only repository contract that the
// gitview server is written against. Implementations exist for local git
// directories (localgit) and the GitHub REST API (ghapi).
package backend

import (
	"context"
	"errors"
	"io"
	"time"
)

// Sentinel errors. Backends wrap these with %w and detail.
var (
	ErrNotFound    = errors.New("not found")
	ErrUnsupported = errors.New("unsupported")
)

// MaxBlobBytes caps what backends load into memory for display.
const MaxBlobBytes = 10 << 20

// Repo is a read-only view of one repository.
// Implementations must be safe for concurrent use.
type Repo interface {
	// Info returns identity and defaults. Called once at startup and cached.
	Info(ctx context.Context) (Info, error)

	// Refs returns all branches and tags with their tip SHAs.
	Refs(ctx context.Context) (Refs, error)

	// Resolve turns a ref name or SHA prefix into a full commit SHA.
	Resolve(ctx context.Context, ref string) (string, error)

	// Tree lists the entries of the directory at path ("" = root) at rev,
	// sorted GitHub-style: dirs first, then files, case-insensitive.
	Tree(ctx context.Context, rev, path string) ([]TreeEntry, error)

	// Blob returns file content and metadata at rev:path.
	Blob(ctx context.Context, rev, path string) (Blob, error)

	// Commits lists up to n commits reachable from rev, newest first,
	// optionally limited to those touching path ("" = all), skipping skip.
	// more reports whether older commits exist beyond the window.
	Commits(ctx context.Context, rev, path string, n, skip int) (commits []Commit, more bool, err error)

	// Commit returns one commit with its full diff against its first
	// parent, or against the empty tree for a root commit.
	Commit(ctx context.Context, sha string) (CommitDetail, error)

	// LastCommit returns the most recent commit touching path at rev.
	LastCommit(ctx context.Context, rev, path string) (Commit, error)

	// Blame attributes every line of rev:path to a commit.
	Blame(ctx context.Context, rev, path string) ([]BlameHunk, error)

	// Files returns every file path at rev (recursive, sorted).
	Files(ctx context.Context, rev string) ([]string, error)

	// Archive streams a zip or tar.gz of the tree at rev. format is
	// "zip" or "tar.gz"; prefix is the top-level directory inside the
	// archive.
	Archive(ctx context.Context, rev, format, prefix string, w io.Writer) error
}

// Info is repository identity.
type Info struct {
	Owner         string
	Name          string
	Description   string
	DefaultBranch string
	CloneURL      string
	Mirror        bool
}

// Refs is the full ref listing.
type Refs struct {
	Branches []Ref
	Tags     []Ref // newest first
}

type Ref struct {
	Name string
	SHA  string
	Date time.Time
}

// EntryKind distinguishes tree rows.
type EntryKind int

const (
	KindFile EntryKind = iota
	KindDir
	KindSymlink
	KindSubmodule
)

type TreeEntry struct {
	Name string
	Path string
	Kind EntryKind
	Size int64 // bytes for files, -1 when unknown
	Mode string
}

// Blob is file content plus the metadata the viewer needs.
type Blob struct {
	Path    string
	Size    int64
	Content []byte
	Binary  bool
	LFS     *LFSPointer
	TooBig  bool
}

type LFSPointer struct {
	OID  string
	Size int64
}

// Signature is an author or committer stamp.
type Signature struct {
	Name      string
	Email     string
	Date      time.Time
	Login     string // GitHub login when known, else ""
	AvatarURL string // ghapi only
}

type Commit struct {
	SHA       string
	Subject   string
	Body      string
	Author    Signature
	Committer Signature
	Parents   []string
}

// DiffStatus matches git --name-status letters.
type DiffStatus string

const (
	Added    DiffStatus = "A"
	Modified DiffStatus = "M"
	Deleted  DiffStatus = "D"
	Renamed  DiffStatus = "R"
	Copied   DiffStatus = "C"
)

type CommitDetail struct {
	Commit
	Stats StatTotal
	Files []FileDiff
}

type StatTotal struct {
	FilesChanged int
	Additions    int
	Deletions    int
}

type FileDiff struct {
	OldPath   string
	NewPath   string
	Status    DiffStatus
	Additions int
	Deletions int
	Binary    bool
	TooLarge  bool
	Hunks     []Hunk
}

type Hunk struct {
	Header   string
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []DiffLine
}

type LineKind int

const (
	Context LineKind = iota
	Addition
	Deletion
)

type DiffLine struct {
	Kind   LineKind
	OldNum int // 0 for additions
	NewNum int // 0 for deletions
	Text   string
	NoEOF  bool
}

type BlameHunk struct {
	Commit    Commit
	StartLine int
	Lines     int
	Prev      string // SHA to reblame before this commit, "" if none
}
