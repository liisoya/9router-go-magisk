package translator

import (
	"encoding/json"
	"testing"
)

func TestGemini4Argon_SamplingParamsStripped(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		hasTemp bool
	}{
		{
			name:    "gemini-4-argon strips temp",
			model:   "gemini-4-argon",
			hasTemp: false,
		},
		{
			name:    "gemini-4-argon-high strips temp",
			model:   "gemini-4-argon-high",
			hasTemp: false,
		},
		{
			name:    "gemini-2.5-pro preserves temp",
			model:   "gemini-2.5-pro",
			hasTemp: true,
		},
		{
			name:    "gemini-3.8-flash preserves temp",
			model:   "gemini-3.8-flash",
			hasTemp: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temp := 0.7
			req := OpenAIRequest{
				Model:       tt.model,
				Temperature: &temp,
				Messages: []OpenAIMessage{
					{Role: "user", Content: "hello"},
				},
			}
			rawReq, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			res, err := TranslateOpenAIToGemini(rawReq)
			if err != nil {
				t.Fatalf("TranslateOpenAIToGemini failed: %v", err)
			}

			var geminiReq struct {
				GenerationConfig json.RawMessage `json:"generationConfig"`
			}
			if err := json.Unmarshal(res, &geminiReq); err != nil {
				t.Fatalf("failed to unmarshal gemini req: %v", err)
			}

			if len(geminiReq.GenerationConfig) == 0 {
				if tt.hasTemp {
					t.Fatalf("expected generationConfig but got empty")
				}
				return
			}

			var gc map[string]any
			if err := json.Unmarshal(geminiReq.GenerationConfig, &gc); err != nil {
				t.Fatalf("failed to unmarshal generationConfig: %v", err)
			}

			_, gotTemp := gc["temperature"]

			if gotTemp != tt.hasTemp {
				t.Errorf("temperature presence = %v, want %v", gotTemp, tt.hasTemp)
			}
		})
	}
}

func TestNormalizeGeminiContents_FunctionResponseLast(t *testing.T) {
	// Test case 1: Gemini-4-argon model merges adjacent user turns and moves FunctionResponse last
	contents := []GeminiContent{
		{
			Role: "user",
			Parts: []GeminiPart{
				{FunctionResponse: &GeminiFunctionResp{Name: "func1"}},
				{Text: "text in first turn"},
			},
		},
		{
			Role: "user",
			Parts: []GeminiPart{
				{FunctionResponse: &GeminiFunctionResp{Name: "func2"}},
				{Text: "text in second turn"},
			},
		},
	}

	normalized := NormalizeGeminiContentsForModel(contents, "gemini-4-argon")
	if len(normalized) == 0 {
		t.Fatalf("expected normalized contents")
	}

	// Should be merged into a single user turn (plus guard turns if bracketed, but since starts with user, just 1 or bracketed)
	// Let's inspect parts
	var allParts []GeminiPart
	for _, c := range normalized {
		allParts = append(allParts, c.Parts...)
	}

	// All Text parts should come first, all FunctionResponse parts should come last
	textSeen := false
	for _, p := range allParts {
		if p.FunctionResponse != nil {
			textSeen = true
		} else if p.Text != "" && p.Text != "..." {
			if textSeen {
				t.Errorf("found Text part after FunctionResponse for Gemini 4 model")
			}
		}
	}

	// Test case 2: Non-Gemini-4 model keeps original order untouched
	contentsNonGemini4 := []GeminiContent{
		{
			Role: "user",
			Parts: []GeminiPart{
				{FunctionResponse: &GeminiFunctionResp{Name: "func1"}},
				{Text: "text first"},
			},
		},
	}
	normalizedNon4 := NormalizeGeminiContentsForModel(contentsNonGemini4, "gemini-3.8-flash")
	if len(normalizedNon4) > 0 && len(normalizedNon4[0].Parts) >= 2 {
		if normalizedNon4[0].Parts[0].FunctionResponse == nil {
			t.Errorf("expected non-Gemini-4 model to keep FunctionResponse first when specified")
		}
	}
}
