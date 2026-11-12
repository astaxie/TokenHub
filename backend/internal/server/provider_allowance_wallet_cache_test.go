package server

import (
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
)

func TestManualWalletReadsUseRevisionFencedCache(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	now := time.Now().UTC()
	wallet := allowance.ManualWallet{Enabled: true, Balance: 50, Unit: "USD", AsOf: now.Add(-time.Hour)}
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: allowance.Policy{Wallet: wallet}})
	if err != nil {
		t.Fatal(err)
	}
	read := func(wallet allowance.ManualWallet, at time.Time) allowance.Snapshot {
		t.Helper()
		a, err := store.withManualAllowance(t.Context(), "a", allowance.Snapshot{}, wallet, at)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	if a := read(wallet, now); a.For("model", now, accountAllowanceMaxAge).Status != allowance.Unknown {
		t.Fatal("uncollected manual wallet inferred spare quota")
	}
	if err := store.refreshManualAllowance(t.Context(), "a", cfg, now); err != nil {
		t.Fatal(err)
	}
	if a := read(wallet, now); a.For("model", now, accountAllowanceMaxAge).Status != allowance.Available || *a.Windows[0].Balance != 50 {
		t.Fatalf("missing cached estimate: %+v", a)
	}
	if a := read(wallet, now.Add(11*time.Minute)); a.For("model", now.Add(11*time.Minute), accountAllowanceMaxAge).Status != allowance.Unknown {
		t.Fatal("stale manual estimate established spare quota")
	}
	wallet.Balance = 100
	newCfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Revision: cfg.Revision, Policy: allowance.Policy{Wallet: wallet}})
	if err != nil {
		t.Fatal(err)
	}
	if a := read(wallet, now); a.For("model", now, accountAllowanceMaxAge).Status != allowance.Unknown {
		t.Fatal("changed wallet policy reused an old estimate")
	}
	if err := store.refreshManualAllowance(t.Context(), "a", cfg, now); AsHTTPError(err).Code != "allowance_config_changed" {
		t.Fatalf("old policy published a wallet estimate: %v", err)
	}
	if err := store.refreshManualAllowance(t.Context(), "a", newCfg, now); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DROP TABLE usage_records").Error; err != nil {
		t.Fatal(err)
	}
	if a := read(wallet, now); *a.Windows[0].Balance != 100 {
		t.Fatal("cached read required the usage ledger")
	}
	if err := store.refreshManualAllowance(t.Context(), "a", newCfg, now.Add(time.Second)); err == nil {
		t.Fatal("background computation ignored a failed ledger read")
	}
	a := read(wallet, now.Add(time.Second))
	if a.For("model", now.Add(time.Second), accountAllowanceMaxAge).Status != allowance.QueryError || *a.Windows[0].Balance != 100 || !a.FetchedAt.Equal(now) {
		t.Fatalf("failed computation lost the prior estimate or its age: %+v", a)
	}
	if err := store.db.Model(&providerAllowanceSetting{}).Where("resource_id = ?", "a").Update("wallet_snapshot", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.refreshManualAllowance(t.Context(), "a", newCfg, now.Add(2*time.Second)); err == nil {
		t.Fatal("first computation ignored a failed ledger read")
	}
	if a := read(wallet, now.Add(2*time.Second)); a.For("model", now.Add(2*time.Second), accountAllowanceMaxAge).Status != allowance.QueryError {
		t.Fatal("first failed computation was reported as unknown")
	}
}
