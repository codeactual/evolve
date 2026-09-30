// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package version

// Version is injected at build time via -ldflags (`make build` stamps it).
//
// Version defaults to dev when no build metadata is supplied.
var Version = "dev"

// Commit may be injected at build time via -ldflags; `make build` does not set
// it.
//
// Commit defaults to none when no build metadata is supplied.
var Commit = "none"

// BuildDate may be injected at build time via -ldflags; `make build` does not
// set it.
//
// BuildDate defaults to unknown when no build metadata is supplied.
var BuildDate = "unknown"
