package auth

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sort"
	"strings"
	"unicode"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type simHashContextKey struct{}

const (
	simhashArrayThreshold  = 6
	simhashArrayKeep       = 3
	simhashStringThreshold = 100
	simhashStringKeep      = 50
)

func ensureRequestSimHashMetadata(opts cliproxyexecutor.Options, selector Selector) cliproxyexecutor.Options {
	if _, ok := selector.(*SimHashSelector); !ok {
		return opts
	}
	if hasRequestSimHashMetadata(opts.Metadata) {
		return opts
	}
	hash, ok := requestSimHash(opts.OriginalRequest)
	if !ok {
		return opts
	}
	if len(opts.Metadata) == 0 {
		opts.Metadata = map[string]any{cliproxyexecutor.RequestSimHashMetadataKey: hash}
		return opts
	}
	meta := make(map[string]any, len(opts.Metadata)+1)
	for k, v := range opts.Metadata {
		meta[k] = v
	}
	meta[cliproxyexecutor.RequestSimHashMetadataKey] = hash
	opts.Metadata = meta
	return opts
}

func withRequestSimHash(ctx context.Context, meta map[string]any) context.Context {
	hash, ok := requestSimHashFromMetadata(meta)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, simHashContextKey{}, hash)
}

func requestSimHashFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}
	hash, ok := ctx.Value(simHashContextKey{}).(uint64)
	return hash, ok
}

func requestSimHash(payload []byte) (uint64, bool) {
	if len(payload) == 0 {
		return 0, false
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return 0, false
	}
	tokens := make([]string, 0, 64)
	collectSimHashTokens("root", compactSimHashValue(value), &tokens)
	if len(tokens) == 0 {
		return 0, false
	}
	var weights [64]int
	for _, token := range tokens {
		sum := fnvHash64(token)
		for bit := 0; bit < 64; bit++ {
			if sum&(uint64(1)<<bit) != 0 {
				weights[bit]++
			} else {
				weights[bit]--
			}
		}
	}
	var out uint64
	for bit, weight := range weights {
		if weight > 0 {
			out |= uint64(1) << bit
		}
	}
	return out, true
}

func compactSimHashValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if shouldIgnoreSimHashKey(key) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(keys))
		for _, key := range keys {
			result[key] = compactSimHashValue(typed[key])
		}
		return result
	case []any:
		items := append([]any{}, typed...)
		if len(items) > simhashArrayThreshold {
			items = append(append([]any{}, typed[:simhashArrayKeep]...), typed[len(typed)-simhashArrayKeep:]...)
		}
		for i := range items {
			items[i] = compactSimHashValue(items[i])
		}
		return items
	case string:
		return compactSimHashString(typed)
	default:
		return value
	}
}

func compactSimHashString(value string) string {
	runes := []rune(value)
	if len(runes) <= simhashStringThreshold {
		return value
	}
	return string(runes[:simhashStringKeep]) + string(runes[len(runes)-simhashStringKeep:])
}

func shouldIgnoreSimHashKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "metadata", "prompt_cache_key", "request_id", "trace_id":
		return true
	default:
		return false
	}
}

func collectSimHashTokens(path string, value any, tokens *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPath := path + "." + key
			*tokens = append(*tokens, "key:"+nextPath)
			collectSimHashTokens(nextPath, typed[key], tokens)
		}
	case []any:
		*tokens = append(*tokens, path+"#len="+itoa(len(typed)))
		for _, item := range typed {
			collectSimHashTokens(path+"[]", item, tokens)
		}
	case string:
		*tokens = append(*tokens, path+"#str")
		for _, token := range splitSimHashString(typed) {
			*tokens = append(*tokens, path+"#"+token)
		}
	case bool:
		if typed {
			*tokens = append(*tokens, path+"=true")
		} else {
			*tokens = append(*tokens, path+"=false")
		}
	case float64:
		*tokens = append(*tokens, path+"#num")
	case nil:
		*tokens = append(*tokens, path+"=null")
	default:
		*tokens = append(*tokens, path+"#other")
	}
}

func splitSimHashString(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(parts) > 12 {
		return append(append([]string{}, parts[:6]...), parts[len(parts)-6:]...)
	}
	return parts
}

func fnvHash64(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

func hasRequestSimHashMetadata(meta map[string]any) bool {
	_, ok := requestSimHashFromMetadata(meta)
	return ok
}

func requestSimHashFromMetadata(meta map[string]any) (uint64, bool) {
	if len(meta) == 0 {
		return 0, false
	}
	raw, ok := meta[cliproxyexecutor.RequestSimHashMetadataKey]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case uint64:
		return v, true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	default:
		return 0, false
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
