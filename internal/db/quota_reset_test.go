package db

import (
	"errors"
	"testing"

	"9router/proxy/internal/handlerutil"
)

// TestQuotaResetMarksTheEndOfHistory proves a reset hands an exhausted key a
// fresh budget without deleting anything: spend recorded after the mark counts,
// spend before it does not, and the history is still there afterwards.
//
// Every insert and both resets in this test land in the same clock second,
// which is exactly the case a timestamp watermark gets wrong — usageHistory
// timestamps are RFC3339 at second precision, so `timestamp >= resetStamp`
// would keep charging a key that was reset the moment it was watched failing.
func TestQuotaResetMarksTheEndOfHistory(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	const rawKey = "sk-quota-reset-key"
	if err := r.CreateApiKey("kr", rawKey, "reset key", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := r.SetApiKeyBudget("kr", 1000, 0); err != nil {
		t.Fatalf("SetApiKeyBudget: %v", err)
	}

	// A key that has never been reset measures against its whole life, and an
	// unknown one reads as 0 rather than erroring.
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "kr"); err != nil || used != 0 {
		t.Fatalf("fresh usage = (%d, %v), want (0, nil)", used, err)
	}
	if mark, err := r.QuotaResetID(t.Context(), "kr"); err != nil || mark != 0 {
		t.Fatalf("fresh reset mark = (%d, %v), want (0, nil)", mark, err)
	}

	// One row in each spelling, exactly as TestGetApiKeyTokenUsageSumsMatchedForms
	// arranges them.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c1", handlerutil.MaskAPIKey(rawKey),
		"/v1/chat/completions", 100, 50, 0, "success", 150, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory masked: %v", err)
	}
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c1", rawKey,
		"/v1/chat/completions", 200, 25, 0, "success", 225, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory raw: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "kr"); err != nil || used != 375 {
		t.Fatalf("usage before reset = (%d, %v), want (375, nil)", used, err)
	}

	mark, err := r.ResetApiKeyQuota(t.Context(), "kr")
	if err != nil {
		t.Fatalf("ResetApiKeyQuota: %v", err)
	}
	if mark <= 0 {
		t.Fatalf("reset mark = %d, want the last usageHistory row id", mark)
	}

	// The budget starts over, through both readers: the limiter's (by key) and
	// the dashboard's (by id).
	if used, err := r.GetApiKeyTokenUsage(rawKey); err != nil || used != 0 {
		t.Errorf("usage by key after reset = (%d, %v), want (0, nil)", used, err)
	}
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "kr"); err != nil || used != 0 {
		t.Errorf("usage by id after reset = (%d, %v), want (0, nil)", used, err)
	}
	if got, err := r.QuotaResetID(t.Context(), "kr"); err != nil || got != mark {
		t.Errorf("QuotaResetID = (%d, %v), want (%d, nil)", got, err, mark)
	}

	// The history survived: the rows are still there for analytics, and the
	// key simply is not charged for them any more.
	var rows int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM usageHistory`).Scan(&rows); err != nil {
		t.Fatalf("count usageHistory: %v", err)
	}
	if rows != 2 {
		t.Errorf("usageHistory rows = %d, want 2 — a reset must not delete history", rows)
	}

	// Spend after the mark counts again.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c1", rawKey,
		"/v1/chat/completions", 10, 5, 0, "success", 15, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory after reset: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "kr"); err != nil || used != 15 {
		t.Fatalf("usage after post-reset spend = (%d, %v), want (15, nil)", used, err)
	}

	// A second reset covers that batch too, and its mark moves forward.
	second, err := r.ResetApiKeyQuota(t.Context(), "kr")
	if err != nil {
		t.Fatalf("second ResetApiKeyQuota: %v", err)
	}
	if second <= mark {
		t.Errorf("second mark = %d, want > %d", second, mark)
	}
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "kr"); err != nil || used != 0 {
		t.Errorf("usage after second reset = (%d, %v), want (0, nil)", used, err)
	}
}

// TestQuotaResetRejectsAnUnknownKey proves the reset reports a key that is not
// there instead of answering success: a 200 would hide a stale dashboard row.
func TestQuotaResetRejectsAnUnknownKey(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	if _, err := r.ResetApiKeyQuota(t.Context(), "does-not-exist"); !errors.Is(err, errNoSuchApiKey) {
		t.Errorf("ResetApiKeyQuota(unknown) error = %v, want errNoSuchApiKey", err)
	}
	if _, err := r.ResetApiKeyQuota(t.Context(), ""); err == nil {
		t.Error("ResetApiKeyQuota(\"\") = nil error, want a refusal")
	}
	if used, err := r.GetApiKeyTokenUsageByID(t.Context(), "does-not-exist"); err != nil || used != 0 {
		t.Errorf("usage for unknown key = (%d, %v), want (0, nil)", used, err)
	}
}

// TestQuotaResetIsIndependentOfTheBudgetColumn proves the reset mark and the
// quota itself are separate writes: resetting must not clear or change the
// configured budget, which is the value the operator set on purpose.
func TestQuotaResetIsIndependentOfTheBudgetColumn(t *testing.T) {
	r := openEphemeralSchemaDB(t)
	if err := r.CreateApiKey("kb", "sk-budget-kept", "budget kept", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}
	if err := r.SetApiKeyBudget("kb", 4242, 30000); err != nil {
		t.Fatalf("SetApiKeyBudget: %v", err)
	}
	if _, err := r.ResetApiKeyQuota(t.Context(), "kb"); err != nil {
		t.Fatalf("ResetApiKeyQuota: %v", err)
	}

	got, err := r.GetApiKeyByID("kb")
	if err != nil || got == nil {
		t.Fatalf("GetApiKeyByID: %v", err)
	}
	assertIntPtr(t, "TokenQuota", got.TokenQuota, 4242)
	assertIntPtr(t, "QueueTimeoutMs", got.QueueTimeoutMs, 30000)
}

// TestTokenUsageAttributesSpendToTheClientKey pins the ledger side of #365:
// spend counts when the row names the CLIENT that sent it, and a row carrying
// only the upstream credential does not.
//
// That second case is the bug this column exists to fix. The gateway recorded
// every request against the provider credential it used, so the sum a
// tokenQuota was measured against stayed 0 forever and no budget could ever
// fire — on a live database, 0 of 634 rows matched any client key.
func TestTokenUsageAttributesSpendToTheClientKey(t *testing.T) {
	r := openEphemeralSchemaDB(t)

	const clientKey = "sk-client-attributed"
	const upstreamKey = "thk-provider-credential"
	const otherClient = "sk-someone-elses"

	if err := r.CreateApiKey("ka", clientKey, "attributed", "m"); err != nil {
		t.Fatalf("CreateApiKey: %v", err)
	}

	// The shape this gateway writes: the provider credential in apiKey, the
	// caller in clientKey.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c1", handlerutil.MaskAPIKey(upstreamKey),
		"/v1/chat/completions", 100, 50, 0, "success", 150, "{}", "{}", handlerutil.MaskAPIKey(clientKey)); err != nil {
		t.Fatalf("InsertUsageHistory with client: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsage(clientKey); err != nil || used != 150 {
		t.Fatalf("attributed usage = (%d, %v), want (150, nil)", used, err)
	}

	// The shape the port wrote before this fix: no client attribution at all.
	// Those tokens belong to the provider account, not to this key.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c2", handlerutil.MaskAPIKey(upstreamKey),
		"/v1/chat/completions", 999, 999, 0, "success", 1998, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory without client: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsage(clientKey); err != nil || used != 150 {
		t.Errorf("usage after an unattributed row = (%d, %v), want (150, nil) — an upstream credential is not a caller", used, err)
	}

	// A row the upstream Next.js dashboard wrote: it stored the CLIENT key in
	// apiKey, so migrated history keeps counting through the fallback term.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c3", clientKey,
		"/v1/chat/completions", 10, 5, 0, "success", 15, "{}", "{}"); err != nil {
		t.Fatalf("InsertUsageHistory legacy shape: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsage(clientKey); err != nil || used != 165 {
		t.Errorf("usage after a legacy row = (%d, %v), want (165, nil)", used, err)
	}

	// Another client's spend never lands on this key.
	if err := r.InsertUsageHistory("openai", "gpt-4o", "c4", handlerutil.MaskAPIKey(upstreamKey),
		"/v1/chat/completions", 500, 500, 0, "success", 1000, "{}", "{}", handlerutil.MaskAPIKey(otherClient)); err != nil {
		t.Fatalf("InsertUsageHistory other client: %v", err)
	}
	if used, err := r.GetApiKeyTokenUsage(clientKey); err != nil || used != 165 {
		t.Errorf("usage after another client spent = (%d, %v), want (165, nil)", used, err)
	}
	// ...while it does land on the client that spent it.
	if used, err := r.GetApiKeyTokenUsage(otherClient); err != nil || used != 1000 {
		t.Errorf("other client usage = (%d, %v), want (1000, nil)", used, err)
	}
}
