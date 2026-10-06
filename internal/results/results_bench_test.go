// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package results

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codeactual/evolve/internal/encfmt"
)

// loadBenchFile decodes the committed 130 KB go-docs results file — a real,
// large, multi-model results document — into a *File for the save/marshal
// benchmarks. It is the worst case the run engine writes after each
// skill/model pair, so it bounds the cost of the per-unit rewrite.
func loadBenchFile(tb testing.TB) *File {
	tb.Helper()
	path := filepath.Join("testdata", "go-docs-results.json")
	var f File
	if err := encfmt.DecodeFile(path, &f); err != nil {
		tb.Fatalf("decode %s: %v", path, err)
	}
	return &f
}

// BenchmarkSaveDir measures the full per-unit rewrite the run engine performs
// after every skill/model pair: marshal the entire results file plus the
// atomic temp-write/rename/stale-sweep. This is the cost the user suspected
// was stalling large sweeps.
func BenchmarkSaveDir(b *testing.B) {
	f := loadBenchFile(b)
	dir := b.TempDir()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := f.SaveDir(dir, "json"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMarshal isolates serialization (no disk) so a slow SaveDir can be
// attributed to encoding vs the filesystem dance around it.
func BenchmarkMarshal(b *testing.B) {
	f := loadBenchFile(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := encfmt.Marshal(f, "json", generatedComment); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeFile measures the load half — LoadDir decodes the same file
// once per skill at the start of a sweep set.
func BenchmarkDecodeFile(b *testing.B) {
	path := filepath.Join("testdata", "go-docs-results.json")
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	tmp := filepath.Join(b.TempDir(), "results.json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var f File
		if err := encfmt.DecodeFile(tmp, &f); err != nil {
			b.Fatal(err)
		}
	}
}
