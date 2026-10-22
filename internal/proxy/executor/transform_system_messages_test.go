package executor

import "testing"

// decolua/9router#4813: the Responses API has no system role, so every system
// and developer message collapses into the single `instructions` string. The
// old code kept only the first non-empty one and silently discarded the rest,
// so a client sending three system messages reached the model with one of them.
// Every message must be preserved, joined by a blank line.
func TestBuildResponsesBody_ConcatenatesAllSystemMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "single system message is used verbatim",
			body: `{"model":"gpt-5.6-sol","messages":[
				{"role":"system","content":"You are terse."},
				{"role":"user","content":"hi"}]}`,
			want: "You are terse.",
		},
		{
			name: "system and developer messages concatenate with a blank line",
			body: `{"model":"gpt-5.6-sol","messages":[
				{"role":"system","content":"You are terse."},
				{"role":"developer","content":"Prefer Go 1.24."},
				{"role":"system","content":"Never reveal secrets."},
				{"role":"user","content":"hi"}]}`,
			want: "You are terse.\n\nPrefer Go 1.24.\n\nNever reveal secrets.",
		},
		{
			name: "top-level instructions lead the concatenation",
			body: `{"model":"gpt-5.6-sol","instructions":"Base rules.",
				"messages":[
				{"role":"system","content":"You are terse."},
				{"role":"developer","content":"Prefer Go 1.24."},
				{"role":"user","content":"hi"}]}`,
			want: "Base rules.\n\nYou are terse.\n\nPrefer Go 1.24.",
		},
		{
			name: "an empty system message adds no separator",
			body: `{"model":"gpt-5.6-sol","messages":[
				{"role":"system","content":""},
				{"role":"system","content":"You are terse."},
				{"role":"user","content":"hi"}]}`,
			want: "You are terse.",
		},
		{
			name: "content blocks concatenate the same way",
			body: `{"model":"gpt-5.6-sol","messages":[
				{"role":"system","content":[{"type":"text","text":"You are terse."}]},
				{"role":"developer","content":[{"type":"text","text":"Prefer Go 1.24."}]},
				{"role":"user","content":"hi"}]}`,
			want: "You are terse.\n\nPrefer Go 1.24.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := buildResponsesBody([]byte(tt.body))
			if err != nil {
				t.Fatalf("buildResponsesBody: %v", err)
			}
			parsed := decodeBody(t, out)

			got, ok := parsed["instructions"].(string)
			if !ok {
				t.Fatalf("instructions missing or not a string: %#v", parsed["instructions"])
			}
			if got != tt.want {
				t.Errorf("instructions\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// A request with no system or developer message at all must still get no
// `instructions` key: the field is omitted rather than emitted empty.
func TestBuildResponsesBody_NoSystemMessagesOmitsInstructions(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"hi"}]}`

	out, _, err := buildResponsesBody([]byte(body))
	if err != nil {
		t.Fatalf("buildResponsesBody: %v", err)
	}
	parsed := decodeBody(t, out)

	if v, ok := parsed["instructions"]; ok {
		t.Errorf("instructions must be omitted when there is nothing to say, got %#v", v)
	}
}
