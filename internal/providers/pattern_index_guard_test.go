package providers

import (
	"strings"
	"testing"
)

func TestPatternTableHasOnlyStarWildcards(t *testing.T) {
	for i := range patternCapabilities {
		p := patternCapabilities[i]
		if strings.ContainsAny(p.pattern, "?[\\") {
			t.Errorf("pattern %q uses a glob metacharacter the fragment prefilter cannot model", p.pattern)
		}
		if !strings.Contains(p.pattern, "*") {
			t.Errorf("pattern %q has no '*' wildcard", p.pattern)
		}
	}
}
