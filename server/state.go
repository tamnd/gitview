package server

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/tamnd/gitview/backend"
)

// capability tracks whether an optional backend method works; it flips to
// capNo the first time a call returns backend.ErrUnsupported so the UI can
// stop offering the feature.
type capability int32

const (
	capUnknown capability = iota
	capYes
	capNo
)

// repoState wraps one backend with identity and small advisory caches.
type repoState struct {
	repo backend.Repo
	info backend.Info

	refMu   sync.Mutex
	refs    backend.Refs
	refTime time.Time

	lastMu   sync.Mutex
	lastSeen map[string]backend.Commit // rev+"\x00"+path, bounded

	capMu    sync.Mutex
	lastCap  capability
	blameCap capability
	filesCap capability
	archCap  capability
}

var unsafeKey = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func newRepoState(ctx context.Context, repo backend.Repo) (*repoState, error) {
	info, err := repo.Info(ctx)
	if err != nil {
		return nil, err
	}
	info.Owner = sanitizeKeyPart(info.Owner, "local")
	info.Name = sanitizeKeyPart(info.Name, "repo")
	return &repoState{
		repo:     repo,
		info:     info,
		lastSeen: make(map[string]backend.Commit),
	}, nil
}

func sanitizeKeyPart(s, fallback string) string {
	s = unsafeKey.ReplaceAllString(s, "-")
	if s == "" || s == "." || s == ".." {
		return fallback
	}
	return s
}

func (rs *repoState) key() string { return rs.info.Owner + "/" + rs.info.Name }

// cachedRefs returns the ref listing, refreshed at most every 5 seconds:
// every page needs it for URL splitting and the branch picker.
func (rs *repoState) cachedRefs(ctx context.Context) (backend.Refs, error) {
	rs.refMu.Lock()
	defer rs.refMu.Unlock()
	if time.Since(rs.refTime) < 5*time.Second {
		return rs.refs, nil
	}
	refs, err := rs.repo.Refs(ctx)
	if err != nil {
		return backend.Refs{}, err
	}
	rs.refs, rs.refTime = refs, time.Now()
	return refs, nil
}

// lastCommit memoizes per rev:path results; SHA-keyed data never changes.
func (rs *repoState) lastCommit(ctx context.Context, rev, path string) (backend.Commit, bool) {
	if rs.capState(&rs.lastCap) == capNo {
		return backend.Commit{}, false
	}
	key := rev + "\x00" + path
	rs.lastMu.Lock()
	c, ok := rs.lastSeen[key]
	rs.lastMu.Unlock()
	if ok {
		return c, true
	}
	c, err := rs.repo.LastCommit(ctx, rev, path)
	if err != nil {
		if errors.Is(err, backend.ErrUnsupported) {
			rs.setCap(&rs.lastCap, capNo)
		}
		return backend.Commit{}, false
	}
	rs.setCap(&rs.lastCap, capYes)
	rs.lastMu.Lock()
	if len(rs.lastSeen) >= 4096 {
		clear(rs.lastSeen)
	}
	rs.lastSeen[key] = c
	rs.lastMu.Unlock()
	return c, true
}

// fillLastCommits fans LastCommit calls over a small pool, GitHub's file
// table being the one place a page needs dozens of history lookups.
func (rs *repoState) fillLastCommits(ctx context.Context, rev string, paths []string) map[string]backend.Commit {
	out := make(map[string]backend.Commit, len(paths))
	if rs.capState(&rs.lastCap) == capNo {
		return out
	}
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, p := range paths {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			if c, ok := rs.lastCommit(ctx, rev, p); ok {
				mu.Lock()
				out[p] = c
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	return out
}

func (rs *repoState) capState(c *capability) capability {
	rs.capMu.Lock()
	defer rs.capMu.Unlock()
	return *c
}

func (rs *repoState) setCap(c *capability, v capability) {
	rs.capMu.Lock()
	*c = v
	rs.capMu.Unlock()
}

// markCap records a method outcome and reports whether it was unsupported.
func (rs *repoState) markCap(c *capability, err error) bool {
	if errors.Is(err, backend.ErrUnsupported) {
		rs.setCap(c, capNo)
		return true
	}
	if err == nil {
		rs.setCap(c, capYes)
	}
	return false
}
