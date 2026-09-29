package providers

import "testing"

func TestClampDeepseekEffort(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		level    string
		want     string
	}{
		{
			name:     "max survives when the model declares it",
			provider: "mimo-free",
			model:    "mimo-v2.5-free",
			level:    "max",
			want:     "max",
		},
		{
			// mimo-v2.5-pro and v2.6 answer 400 to "max" on the Go lane (upstream
			// probed this live); v2.5 accepts it, so the clamp has to key off the
			// declared levels rather than the model name.
			name:     "max downgrades when the level set omits it",
			provider: "mimo-free",
			model:    "mimo-v2.5-pro",
			level:    "max",
			want:     "high",
		},
		{
			name:     "xhigh downgrades the same way",
			provider: "mimo-free",
			model:    "mimo-v2.6-flash",
			level:    "xhigh",
			want:     "high",
		},
		{
			name:     "high is already the floor for xhigh models",
			provider: "mimo-free",
			model:    "mimo-v2.5-pro",
			level:    "high",
			want:     "high",
		},
		{
			name:     "medium maps to high",
			provider: "mimo-free",
			model:    "mimo-v2.5-pro",
			level:    "medium",
			want:     "high",
		},
		{
			name:     "low maps to high",
			provider: "mimo-free",
			model:    "mimo-v2.5-pro",
			level:    "low",
			want:     "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClampDeepseekEffort(tt.provider, tt.model, tt.level); got != tt.want {
				t.Errorf("ClampDeepseekEffort(%q, %q, %q) = %q, want %q",
					tt.provider, tt.model, tt.level, got, tt.want)
			}
		})
	}
}

// A level set that declares no "max" at all must still be downgraded rather than
// passing "max" through: an absent level set means the model never advertised max.
func TestClampDeepseekEffort_UndeclaredMaxIsDowngraded(t *testing.T) {
	if got := ClampDeepseekEffort("no-such-provider", "no-such-model", "max"); got != "high" {
		t.Errorf("undeclared model: got %q, want high", got)
	}
}
