package executor

import (
	json "encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DSML is the tool-call dialect the site models actually speak.
//
// The prompt asks for a single JSON object and a model follows it often enough to
// work — but these models have a native tool syntax they prefer, and when they
// reach for it the answer comes back as a DSML block instead. Passing that
// through as content is the worst outcome available: the client sees markup
// ending in `parameter>`, has no call to run, and concludes the model refused to
// answer. So the block is parsed here and returned as an ordinary call.
//
//	<｜｜DSML｜｜ calls>
//	<｜｜DSML｜｜ invoke name="read">
//	<｜｜DSML｜｜ parameter name="path" string="true">https://wttr.in/Jakarta</｜｜DSML｜｜ parameter>
//	</｜｜DSML｜｜ invoke>
//	</｜｜DSML｜｜ calls>
//
// Four spelling variations are in the wild and all of them are matched here: the
// bar may be full-width (U+FF5C) or ASCII, the marker may be padded with spaces,
// and a closing tag may or may not carry its slash.
//
// The slash is the one that matters. Closing tags were first matched against the
// opener spelled without one — `<｜｜DSML｜｜ invoke>` — which matches nothing in a
// real answer, whose tags close as `</｜｜DSML｜｜ invoke>`. `strings.Index` returned
// -1, the whole block stayed unparsed, and every DSML call reached the client as
// literal markup. A parser must key on what the model writes, not on how the
// notation reads when described.
//
// The `calls` element matters for the same reason. A model wraps its invokes in
// one, and an extractor that consumed only the invoke left the wrapper behind.
// The caller kept the text around the call as the assistant's preamble — which is
// correct for prose — so `<｜｜DSML｜｜ calls>` and its closer were handed to the
// client as content. Every turn showed the user an empty pair of tags.

var (
	// deepSeekDSMLMarker recognises the namespace in any spelling, and is what
	// decides whether an answer is worth examining at all.
	deepSeekDSMLMarker = regexp.MustCompile(`[｜|]{1,2}\s*DSML\s*[｜|]{1,2}`)

	// An opening tag is `<` followed by the namespace. It cannot match a closing
	// tag, whose `<` is followed by `/` — so a scan for openers never trips over
	// a terminator.
	deepSeekDSMLBlockOpen     = regexp.MustCompile(`<[｜|]{1,2}\s*DSML\s*[｜|]{1,2}`)
	deepSeekDSMLCallsOpen     = regexp.MustCompile(`<[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*calls\b`)
	deepSeekDSMLInvokeOpen    = regexp.MustCompile(`<[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*invoke\b`)
	deepSeekDSMLParameterOpen = regexp.MustCompile(`<[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*parameter\b`)

	// A closing tag may or may not carry the slash, so `/?` is deliberate.
	deepSeekDSMLCallsClose     = regexp.MustCompile(`</?[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*calls\s*>`)
	deepSeekDSMLInvokeClose    = regexp.MustCompile(`</?[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*invoke\s*>`)
	deepSeekDSMLParameterClose = regexp.MustCompile(`</?[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*parameter\s*>`)
)

// deepSeekHasDSML reports whether an answer contains the namespace at all.
func deepSeekHasDSML(text string) bool {
	return deepSeekDSMLMarker.MatchString(text)
}

// deepSeekExtractDSML recovers every tool call written in DSML.
//
// All of them, not just the first: a model asked about two cities writes two
// invokes in one answer, and returning only the first costs the client a whole
// round trip per call — and leaves the second invoke sitting in the answer as
// markup, which is the leak this function exists to prevent.
//
// Only invokes naming a tool the client declared are returned. A model that
// invents a tool name is passed over — the same conservative rule the JSON path
// follows, for the same reason: guessing produces an invocation the client then
// acts on.
func deepSeekExtractDSML(text string, tools []deepSeekToolSpec) []deepSeekToolRequest {
	invokes := deepSeekDSMLInvokes(text)
	if len(invokes) == 0 {
		return nil
	}

	calls := make([]deepSeekToolRequest, 0, len(invokes))
	for _, invoke := range invokes {
		call, ok := deepSeekParseDSMLInvoke(invoke, tools)
		if !ok {
			continue
		}
		// A model that repeats the same invoke in one answer would otherwise
		// have the client execute it twice.
		if slices.ContainsFunc(calls, func(existing deepSeekToolRequest) bool {
			return existing.Name == call.Name && existing.Arguments == call.Arguments
		}) {
			continue
		}
		calls = append(calls, call)
	}
	if len(calls) == 0 {
		return nil
	}
	return calls
}

// deepSeekDSMLInvokes returns every invoke element in the answer, verbatim.
//
// The text is returned with its markers because the caller strips the call out of
// the answer to recover whatever the model wrote around it, and that only works if
// the removed text is exactly what was in the answer.
func deepSeekDSMLInvokes(text string) []string {
	var invokes []string
	rest := text
	for {
		openAt := deepSeekDSMLInvokeOpen.FindStringIndex(rest)
		if openAt == nil {
			return invokes
		}
		rest = rest[openAt[0]:]

		// The opening tag ends at its first `>`; the attributes sit between the
		// matched name and that bracket.
		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return invokes
		}
		head := rest[:openEnd+1]
		rest = rest[openEnd+1:]

		closeAt := deepSeekDSMLInvokeClose.FindStringIndex(rest)
		if closeAt == nil {
			return invokes
		}
		invokes = append(invokes, head+rest[:closeAt[1]])
		rest = rest[closeAt[1]:]
	}
}

// deepSeekParseDSMLInvoke turns one invoke element into a call.
func deepSeekParseDSMLInvoke(invoke string, tools []deepSeekToolSpec) (deepSeekToolRequest, bool) {
	attributes, body, found := strings.Cut(invoke, ">")
	if !found {
		return deepSeekToolRequest{}, false
	}

	name := deepSeekDSMLAttribute(attributes, "name")
	if name == "" || !deepSeekToolDeclared(name, tools) {
		return deepSeekToolRequest{}, false
	}

	return deepSeekToolRequest{
		Name:      name,
		Arguments: deepSeekDSMLArguments(body),
		Raw:       invoke,
	}, true
}

// deepSeekDSMLAttribute reads one quoted attribute value.
//
// The name is matched only where it is a whole attribute, so a `name=` nested in
// a longer attribute name cannot be mistaken for it.
func deepSeekDSMLAttribute(attributes, key string) string {
	marker := key + "="
	rest := attributes
	for {
		at := strings.Index(rest, marker)
		if at < 0 {
			return ""
		}
		// The preceding character decides whether this is an attribute of its own
		// or the tail of a longer one, so it is read before the slice moves.
		wholeAttribute := at == 0 || isDSMLAttrBreak(rest[at-1])
		rest = rest[at+len(marker):]
		if !wholeAttribute {
			continue
		}
		if rest == "" {
			return ""
		}
		quote := rest[0]
		if quote != '"' && quote != '\'' {
			return ""
		}
		value, _, ok := strings.Cut(rest[1:], string(quote))
		if !ok {
			return ""
		}
		return value
	}
}

func isDSMLAttrBreak(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// deepSeekDSMLArguments builds the argument object from the parameter elements.
//
// Order is not part of the format, so the properties are emitted sorted by name.
// That keeps the result byte-identical across identical turns, which matters
// because the arguments string is compared when deciding whether a call repeats.
func deepSeekDSMLArguments(body string) string {
	params := deepSeekDSMLParameters(body)

	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteString("{")
	for i, name := range names {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q:%s", name, deepSeekDSMLValue(params[name]))
	}
	b.WriteString("}")
	return b.String()
}

// deepSeekDSMLParameters returns every parameter element's body, keyed by name.
func deepSeekDSMLParameters(body string) map[string]string {
	params := make(map[string]string)
	rest := body
	for {
		openAt := deepSeekDSMLParameterOpen.FindStringIndex(rest)
		if openAt == nil {
			return params
		}
		rest = rest[openAt[1]:]

		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return params
		}
		attributes := rest[:openEnd]
		rest = rest[openEnd+1:]

		closeAt := deepSeekDSMLParameterClose.FindStringIndex(rest)
		if closeAt == nil {
			return params
		}
		if name := deepSeekDSMLAttribute(attributes, "name"); name != "" {
			params[name] = rest[:closeAt[0]]
		}
		rest = rest[closeAt[1]:]
	}
}

// deepSeekDSMLValue renders one parameter body as a JSON value.
//
// Every DSML value is a string; `string="true"` is an attribute describing the
// body, not a JSON type. Emitting the body verbatim as a string is what the
// format carries, and coercing it to a number or bool here would mean guessing
// the tool's schema from the model's prose.
func deepSeekDSMLValue(body string) string {
	encoded, err := json.Marshal(strings.TrimSpace(body))
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// deepSeekStripDSML removes a DSML element from an answer.
//
// Used wherever the markup could otherwise reach the user: a block that could not
// be turned into a call, and the text around a block that could.
//
// The whole element goes, from its opening tag to its terminator, because what
// lies between them is markup too — stripping only the tags would leave the
// parameter bodies behind, so `https://wttr.in/Jakarta` would appear as prose the
// model never wrote. An element that was never closed is treated as running to
// the end of the answer: the model was cut off mid-call, and everything after the
// marker is unfinished markup rather than a sentence.
func deepSeekStripDSML(text string) string {
	for _, element := range []struct {
		open  *regexp.Regexp
		close *regexp.Regexp
	}{
		{deepSeekDSMLCallsOpen, deepSeekDSMLCallsClose},
		{deepSeekDSMLInvokeOpen, deepSeekDSMLInvokeClose},
		{deepSeekDSMLParameterOpen, deepSeekDSMLParameterClose},
	} {
		openAt := element.open.FindStringIndex(text)
		if openAt == nil {
			continue
		}
		// The search for the terminator begins after the opening tag. Starting at
		// the element itself would match that very tag — the close pattern admits
		// a missing slash — and truncate the element at its own first tag,
		// leaving the rest of the markup in place.
		searchFrom := openAt[1]
		if tagEnd := strings.Index(text[searchFrom:], ">"); tagEnd >= 0 {
			searchFrom += tagEnd + 1
		}
		if end := element.close.FindStringIndex(text[searchFrom:]); end != nil {
			return strings.TrimSpace(text[:openAt[0]] + text[searchFrom+end[1]:])
		}
		return strings.TrimSpace(text[:openAt[0]])
	}
	return text
}

// deepSeekDSMLInvokeCount reports how many invokes an answer carries, for the log.
func deepSeekDSMLInvokeCount(text string) int {
	return len(deepSeekDSMLInvokeOpen.FindAllStringIndex(text, -1))
}
