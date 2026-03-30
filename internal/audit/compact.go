package audit

import "encoding/json"

const (
	compactArrayKeep       = 3
	compactArrayThreshold  = 6
	compactStringThreshold = 100
	compactStringKeep      = 50
)

func compactJSONPayload(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}

	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return payload
	}

	compacted, changed := compactJSONValue(value)
	if !changed {
		return payload
	}

	encoded, err := json.Marshal(compacted)
	if err != nil {
		return payload
	}
	return encoded
}

func compactJSONValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			next, itemChanged := compactJSONValue(item)
			result[key] = next
			changed = changed || itemChanged
		}
		return result, changed
	case []any:
		changed := false
		items := typed
		if len(items) > compactArrayThreshold {
			items = append(append([]any{}, typed[:compactArrayKeep]...), typed[len(typed)-compactArrayKeep:]...)
			changed = true
		} else {
			items = append([]any{}, typed...)
		}
		for i, item := range items {
			next, itemChanged := compactJSONValue(item)
			items[i] = next
			changed = changed || itemChanged
		}
		return items, changed
	case string:
		runes := []rune(typed)
		if len(runes) <= compactStringThreshold {
			return typed, false
		}
		head := string(runes[:compactStringKeep])
		tail := string(runes[len(runes)-compactStringKeep:])
		return head + tail, true
	default:
		return value, false
	}
}
