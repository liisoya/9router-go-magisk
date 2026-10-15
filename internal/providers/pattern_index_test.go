package providers

import (
	"strings"
	"testing"
)

func oldPatternFirstMatch(model string) int {
	for i := range patternCapabilities {
		if matchPattern(patternCapabilities[i].pattern, model) {
			return i
		}
	}
	return -1
}

func indexedPatternFirstMatch(model string) int {
	lower := strings.ToLower(model)
	for i := range patternCapabilities {
		p := &patternCapabilities[i]
		if !fragsMatch(p, lower) {
			continue
		}
		if matchPattern(p.pattern, model) {
			return i
		}
	}
	return -1
}

func TestPatternIndexAgreesWithLinearScan(t *testing.T) {
	checked := 0
	for provider, ids := range ProviderModels {
		for _, id := range ids {
			for _, candidate := range []string{id, provider + "/" + id} {
				want := oldPatternFirstMatch(candidate)
				got := indexedPatternFirstMatch(candidate)
				if want != got {
					t.Errorf("model %q: linear scan picks pattern %d (%q), indexed picks %d",
						candidate, want, patternOf(want), got)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no registry models checked")
	}
	t.Logf("checked %d candidates", checked)
}

func patternOf(i int) string {
	if i < 0 || i >= len(patternCapabilities) {
		return "<none>"
	}
	return patternCapabilities[i].pattern
}

func BenchmarkPatternMatchIndexed(b *testing.B) {
	models := registryModelSamples()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indexedPatternFirstMatch(models[i%len(models)])
	}
}

func BenchmarkPatternMatchLinear(b *testing.B) {
	models := registryModelSamples()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oldPatternFirstMatch(models[i%len(models)])
	}
}

func registryModelSamples() []string {
	var out []string
	for provider, ids := range ProviderModels {
		for _, id := range ids {
			out = append(out, id, provider+"/"+id)
		}
	}
	if len(out) == 0 {
		panic("no registry models")
	}
	return out
}
