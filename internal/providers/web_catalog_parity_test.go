package providers

import (
	"os"
	"regexp"
	"testing"
)

// The dashboard renders a provider's "Available Models" rows from
// getModelsByProviderId() in web/src/lib/models.ts — a hand-maintained copy of
// the registry, not a projection of this catalog. The two are updated by
// different edits, and nothing compared them, so a model added here alone stayed
// invisible in the one UI that configures it while /v1/models answered with it.
//
// That is not hypothetical: it is how gemini-4-argon shipped. #379 and #382 both
// grew ProviderModels and neither touched models.ts, so the gateway routed a
// model the dashboard would not list. #353 hit the same shape for gemini-web and
// recorded it in its commit message, and the next model PR repeated it.
//
// models.ts cannot simply be derived from this side: it carries 23 media
// providers (tts, stt, image) that ProviderModels does not, and it resolves
// through PROVIDER_ID_TO_ALIAS. The parity that matters is per provider, so this
// test walks every provider the two files share and fails when the Go catalog
// serves an id the dashboard copy omits.
//
// One direction only, deliberately. models.ts is allowed to list ids Go does not
// (a dashboard row for a provider whose catalog is fetched live), and the reverse
// — a row the dashboard offers that Go would refuse to route — is already caught
// by the routing tests. What is silent by construction is the direction below.
//
// This reads the TypeScript source rather than importing it: the dashboard bundle
// is a separate build, and parsing the object literal here would drag Vite, Bun
// and the whole SPA toolchain into the Go test binary. Extracting the "id" values
// from the block is enough to answer the question this test asks, and it keeps
// failing for the right reason if the block shape changes.
func TestDashboardCatalogListsEveryRoutedModel(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/lib/models.ts")
	if err != nil {
		t.Fatalf("read dashboard catalog: %v", err)
	}
	src := string(raw)

	// Go keys the catalog by both the provider id and its short alias, and
	// models.ts keys its blocks by whichever of the two upstream publishes. The
	// alias table lives in each file, so the pairings that differ are named here.
	feKey := map[string]string{
		"antigravity":  "ag",
		"gemini-cli":   "gc",
		"ocg":          "opencode-go",
		"codebuddy-cn": "cbcn",
	}
	checked := 0
	for pid, models := range ProviderModels {
		key := pid
		if mapped, ok := feKey[pid]; ok {
			key = mapped
		}
		listed := dashboardCatalogIDs(src, key)
		if len(listed) == 0 {
			// No dashboard block for this provider: it is configured through
			// another surface (a live catalog fetch, or a media-only entry).
			continue
		}
		checked++

		var missing []string
		for _, m := range models {
			if !listed[m] {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			t.Errorf("provider %q is served by the gateway but absent from web/src/lib/models.ts block %q: %v\n"+
				"a model the gateway routes must be listed in the dashboard that configures it; "+
				"add it to the %q block (see internal/providers/registry_models.go)",
				pid, key, missing, key)
		}
	}
	if checked == 0 {
		t.Fatal("no provider had a dashboard block: models.ts moved or changed shape, so this test proved nothing")
	}
	t.Logf("checked %d providers present in both catalogs", checked)
}

// dashboardCatalogIDs returns the set of "id" values declared in the
// `"<key>": [ ... ]` block of a models.ts-shaped source. An absent block returns
// an empty set, which the caller treats as "not listed here".
func dashboardCatalogIDs(src, key string) map[string]bool {
	seg := dashboardCatalogBlock(src, key)
	if seg == "" {
		return nil
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`"id":\s*"([^"]+)"`).FindAllStringSubmatch(seg, -1) {
		ids[m[1]] = true
	}
	return ids
}

// dashboardCatalogBlock slices out the body of a top-level `"key": [` entry,
// matching brackets so a nested array (params, supportedFormats) cannot end it
// early.
func dashboardCatalogBlock(src, key string) string {
	loc := regexp.MustCompile(`(?m)^  "` + regexp.QuoteMeta(key) + `": \[`).FindStringIndex(src)
	if loc == nil {
		return ""
	}
	depth := 0
	for i := loc[1] - 1; i < len(src); i++ {
		switch src[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return src[loc[1]:i]
			}
		}
	}
	return ""
}
