package dbupgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// RenderText writes the plan in the maintenance-command house style: fixed
// sections, aligned columns, and a final readiness verdict.
func RenderText(w io.Writer, plan *Plan) {
	_, _ = fmt.Fprintf(w, "source:              %s (%d table(s), %d row(s))\n",
		plan.Source.Driver, len(plan.Source.Tables), plan.Source.TotalRows)
	for _, group := range []struct {
		name  string
		value TableGroup
	}{
		{"protected", TableGroupProtected},
		{"history", TableGroupHistory},
		{"system", TableGroupSystem},
		{"other", TableGroupOther},
	} {
		tables := tablesInGroup(plan, group.value)
		if len(tables) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "  %s:\n", group.name)
		for _, table := range tables {
			encrypted := strings.Join(table.EncryptedColumns, ", ")
			if encrypted != "" {
				encrypted = "  encrypted: " + encrypted
			}
			_, _ = fmt.Fprintf(w, "    %-40s %8d row(s)%s\n", table.Name, table.RowCount, encrypted)
		}
	}

	_, _ = fmt.Fprintf(w, "secret key:          %s\n", plan.Secret.KeySource)
	for _, report := range plan.Secret.Canary {
		status := "ok"
		if report.FailedCiphertexts > 0 {
			status = fmt.Sprintf("%d FAILED", report.FailedCiphertexts)
		}
		_, _ = fmt.Fprintf(w, "  %-42s %6d row(s), %5d value(s), %-9s [%s]\n",
			report.Table+"."+report.Column, report.RowsScanned, report.DistinctCiphertexts, status, report.Severity)
	}
	if plan.Secret.KeySource == string(SecretKeySourceNone) && len(plan.Secret.Canary) == 0 {
		_, _ = fmt.Fprintln(w, "  ciphertext canary: skipped (no secret key)")
	}
	_, _ = fmt.Fprintf(w, "ciphertext canary:   %t\n", plan.Secret.Verified)

	_, _ = fmt.Fprintf(w, "target:              %s\n", plan.Target.Driver)
	_, _ = fmt.Fprintf(w, "  state:             %s", plan.Target.State)
	if plan.Target.LedgerVersion > 0 {
		_, _ = fmt.Fprintf(w, " (ledger version %d)", plan.Target.LedgerVersion)
	}
	_, _ = fmt.Fprintln(w)
	if plan.Target.Detail != "" {
		_, _ = fmt.Fprintf(w, "  detail:            %s\n", plan.Target.Detail)
	}

	if len(plan.Blockers) > 0 {
		_, _ = fmt.Fprintf(w, "blockers (%d):\n", len(plan.Blockers))
		for _, blocker := range plan.Blockers {
			_, _ = fmt.Fprintf(w, "  - %s\n", blocker)
		}
	}
	if len(plan.Warnings) > 0 {
		_, _ = fmt.Fprintf(w, "warnings (%d):\n", len(plan.Warnings))
		for _, warning := range plan.Warnings {
			_, _ = fmt.Fprintf(w, "  - %s\n", warning)
		}
	}

	verdict := "READY (no blockers found; re-run after upgrade-run ships to execute the copy)"
	if len(plan.Blockers) > 0 {
		verdict = "NOT READY: resolve the blockers above"
	}
	_, _ = fmt.Fprintf(w, "result:              %s\n", verdict)
}

// RenderJSON writes the plan as indented JSON; the upgrade-run and
// upgrade-verify commands consume this shape as their contract.
func RenderJSON(w io.Writer, plan *Plan) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

func tablesInGroup(plan *Plan, group TableGroup) []TableReport {
	var tables []TableReport
	for _, table := range plan.Source.Tables {
		if table.Group == string(group) {
			tables = append(tables, table)
		}
	}
	return tables
}
