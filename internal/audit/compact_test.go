package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactJSONPayloadArrayAndString(t *testing.T) {
	longText := strings.Repeat("a", 60) + strings.Repeat("b", 60)
	payload := []byte(`{"items":[1,2,3,4,5,6,7],"text":"` + longText + `"}`)

	compacted := compactJSONPayload(payload)

	var got map[string]any
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatalf("unmarshal compacted payload: %v", err)
	}

	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items type = %T", got["items"])
	}
	if len(items) != 6 {
		t.Fatalf("len(items) = %d, want 6", len(items))
	}
	wantItems := []float64{1, 2, 3, 5, 6, 7}
	for i, want := range wantItems {
		if items[i] != want {
			t.Fatalf("items[%d] = %v, want %v", i, items[i], want)
		}
	}

	text, ok := got["text"].(string)
	if !ok {
		t.Fatalf("text type = %T", got["text"])
	}
	if len([]rune(text)) != 100 {
		t.Fatalf("len(text) = %d, want 100", len([]rune(text)))
	}
	if !strings.HasPrefix(text, strings.Repeat("a", 50)) {
		t.Fatalf("text prefix mismatch: %q", text[:50])
	}
	if !strings.HasSuffix(text, strings.Repeat("b", 50)) {
		t.Fatalf("text suffix mismatch: %q", text[len(text)-50:])
	}
}

func TestCompactJSONPayloadNested(t *testing.T) {
	longText := strings.Repeat("x", 101)
	payload := []byte(`{"outer":[{"inner":["a","b","c","d","e","f","g"],"text":"` + longText + `"}]}`)

	compacted := compactJSONPayload(payload)

	var got map[string]any
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatalf("unmarshal compacted payload: %v", err)
	}

	outer := got["outer"].([]any)
	innerObj := outer[0].(map[string]any)
	inner := innerObj["inner"].([]any)
	if len(inner) != 6 {
		t.Fatalf("len(inner) = %d, want 6", len(inner))
	}
	text := innerObj["text"].(string)
	if len([]rune(text)) != 100 {
		t.Fatalf("len(text) = %d, want 100", len([]rune(text)))
	}
}

func TestCompactJSONPayloadNonJSON(t *testing.T) {
	payload := []byte("not-json")
	compacted := compactJSONPayload(payload)
	if string(compacted) != string(payload) {
		t.Fatalf("non-json payload changed: %q", compacted)
	}
}
