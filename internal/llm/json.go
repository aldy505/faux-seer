package llm

import "encoding/json"

// ExtractJSON decodes the first complete JSON object or array found in a model
// completion into out.
//
// Models routinely wrap structured output in prose or a code fence, so callers
// cannot unmarshal the raw text. It reports whether a value was decoded; out is
// left untouched when none was.
func ExtractJSON(text string, out any) bool {
	if start := firstJSONStart(text); start >= 0 {
		if end := jsonEnd(text, start); end > start {
			if err := json.Unmarshal([]byte(text[start:end]), out); err == nil {
				return true
			}
		}
	}
	// A completion that is already bare JSON needs no extraction.
	return json.Unmarshal([]byte(text), out) == nil
}

// firstJSONStart returns the offset of the first object or array opener.
func firstJSONStart(text string) int {
	for index, character := range text {
		if character == '{' || character == '[' {
			return index
		}
	}
	return -1
}

// jsonEnd returns the offset just past the value opened at start, or -1 when the
// value is unterminated. Strings and their escapes are skipped so braces inside
// string literals do not affect the depth count.
func jsonEnd(text string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(text); index++ {
		character := text[index]
		switch {
		case escaped:
			escaped = false
		case character == '\\' && inString:
			escaped = true
		case character == '"':
			inString = !inString
		case inString:
		case character == '{' || character == '[':
			depth++
		case character == '}' || character == ']':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return -1
}
