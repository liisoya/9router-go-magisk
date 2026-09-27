package providers

import (
	"testing"
)

func TestGpt6Astra_Capabilities(t *testing.T) {
	caps := GetCapabilitiesForModel("codex", "gpt-6-astra")
	if !caps.Vision || !caps.Reasoning || !caps.Search || !caps.Tools {
		t.Errorf("gpt-6-astra should have Vision+Reasoning+Search+Tools, got %+v", caps)
	}

	// Pattern match test
	capsPattern := GetCapabilitiesForModel("", "custom-gpt-6-test")
	if !capsPattern.Vision || !capsPattern.Reasoning || !capsPattern.Search || !capsPattern.Tools {
		t.Errorf("custom-gpt-6-test should match *gpt-6* pattern, got %+v", capsPattern)
	}

	cw, maxOut := GetModelTokenLimits("gpt-6-astra")
	if cw != 272000 || maxOut != 128000 {
		t.Errorf("gpt-6-astra limits expected (272000, 128000), got (%d, %d)", cw, maxOut)
	}
}

func TestGpt56Image_Capabilities(t *testing.T) {
	for _, m := range []string{"gpt-5.6-sol-image", "gpt-5.6-terra-image", "gpt-5.6-luna-image"} {
		caps := GetCapabilitiesForModel("codex", m)
		if !caps.ImageOutput || !caps.Tools {
			t.Errorf("%s should have ImageOutput+Tools, got %+v", m, caps)
		}
	}
}

func TestCodeBuddyCN_V069_Capabilities(t *testing.T) {
	caps := GetCapabilitiesForModel("codebuddy-cn", "glm-5.2")
	if !caps.Vision || !caps.Reasoning || !caps.Tools {
		t.Errorf("codebuddy-cn glm-5.2 should have Vision+Reasoning+Tools, got %+v", caps)
	}
}
