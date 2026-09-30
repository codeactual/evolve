// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
)

// outf, outln and outp write to a command's terminal streams. Write errors are
// dropped on purpose: stdout/stderr of a CLI that is about to exit offer no
// recovery, and aborting mid-report would hide the result the caller is
// waiting for.
func outf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

func outln(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) }

func outp(w io.Writer, a ...any) { _, _ = fmt.Fprint(w, a...) }
