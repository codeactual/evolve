// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codeactual/evolve/internal/evalspec"
	"github.com/codeactual/evolve/internal/manifest"
)

// Content fingerprints let --modified rerun a case whose authored content
// changed since its results were stored. Two granularities are persisted: a
// per-entry content hash (the skill content a tier depends on) on the results
// Header, and a per-case spec hash (the authored trigger/eval JSON) on each
// result. A case is "modified" when either differs from the stored value — see
// triggerCaseReason/evalCaseReason. The hashes are content-only (never mode or
// mtime) so they are stable across checkouts.

// noFrontmatter is hashed in place of a missing frontmatter block, so a SKILL.md
// with no frontmatter still has a stable, distinct content hash.
const noFrontmatter = "\x00evolve:no-frontmatter\x00"

// triggerContentHash fingerprints the skill content a trigger eval depends on:
// the SKILL.md frontmatter, which is what decides whether the skill triggers.
// skillMD is the already-read SKILL.md bytes.
func triggerContentHash(skillMD []byte) string {
	block, ok := manifest.FrontmatterBlock(skillMD)
	if !ok {
		block = []byte(noFrontmatter)
	}
	sum := sha256.Sum256(block)
	return hex.EncodeToString(sum[:])
}

// skillContentHash fingerprints the entire skill directory — every regular file
// an agent would see, since workspace.New symlinks the whole dir in. Files are
// hashed in sorted relative-path order with NUL framing, so neither file order
// nor a path/content boundary can shift the digest without changing content.
// Dotfiles and dot-directories (.DS_Store, editor cruft, VCS metadata) are
// skipped as non-authored noise; symlinks and other irregular files are ignored.
func skillContentHash(skillDir string) (string, error) {
	var rels []string
	err := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != skillDir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// specHash fingerprints one authored trigger or eval definition by hashing its
// JSON marshaling. evalspec.Trigger and evalspec.Eval are scalar/slice-only, so
// json.Marshal is deterministic. For evals the value is the normalized Eval
// (expectations already expanded into assertions) — what actually runs. Marshal
// cannot fail for these types; on the impossible error both stored and fresh
// hashes degrade to the empty digest and simply compare equal (never a false
// "modified").
func specHash(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// evalFingerprint fingerprints an eval as a unit of work: its authored definition
// (specHash) plus the content of every fixture file it stages. Folding fixtures
// in means a changed fixture counts as a changed eval — it reruns the eval under
// --modified and recomputes its without-skill baseline (which depends only on the
// eval, never on the skill). Files are hashed in sorted Dest order with NUL
// framing, like skillContentHash, so neither order nor a path/content boundary
// can shift the digest without changing content; an unreadable fixture folds its
// path and the error in, so a vanished fixture still changes the hash.
//
// This supersedes specHash for evals: it is what EvalResult.SpecHash now stores.
// Old results carry a spec-only hash, so the first --modified after upgrade reruns
// every eval once (a harmless refresh), the same meaning-shift caveat the schema
// already documents.
func evalFingerprint(c evalspec.Eval) string {
	h := sha256.New()
	h.Write([]byte(specHash(c)))
	h.Write([]byte{0})
	files := append([]evalspec.FileRef(nil), c.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Dest < files[j].Dest })
	for _, f := range files {
		h.Write([]byte(f.Dest))
		h.Write([]byte{0})
		if data, err := os.ReadFile(f.Source); err != nil {
			h.Write([]byte("\x00evolve:fixture-error:" + err.Error() + "\x00"))
		} else {
			h.Write(data)
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
