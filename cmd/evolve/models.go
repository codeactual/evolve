// Copyright 2026 BitWise Media Group Ltd
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/codeactual/evolve/internal/version"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Print the effective model matrix with pricing, harnesses, and provenance",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		models, err := opts.ConfiguredModels()
		if err != nil {
			return err
		}
		_, overridden, err := opts.ModelOverrides()
		if err != nil {
			return err
		}
		available, err := opts.AvailableHarnesses()
		if err != nil {
			return err
		}
		avail := map[string]bool{}
		for _, h := range available {
			avail[h.ID()] = true
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		outln(w, "MODEL\tNAME\tINPUT $/MTOK\tOUTPUT $/MTOK\tHARNESSES\tPREFERRED\tSTATUS\tSOURCE")
		for _, m := range models {
			source := "builtin@" + version.Version
			if overridden[m.ProviderID] {
				if source = opts.ConfigFileName(); source == "" {
					source = "config"
				}
			}
			status := "no harness on PATH"
			for id := range m.Supported {
				if avail[id] {
					status = "runnable"
					break
				}
			}
			outf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				m.ID, m.Name, fmtPrice(m.InputUSD), fmtPrice(m.OutputUSD),
				strings.Join(m.SupportedHarnessIDs(), ","), m.Preferred, status, source)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(modelsCmd)
}

func fmtPrice(p *float64) string {
	if p == nil {
		return "unpublished"
	}
	return fmt.Sprintf("%.2f", *p)
}
