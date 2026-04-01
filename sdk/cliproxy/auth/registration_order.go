package auth

import (
	"sort"
	"time"
)

// FirstRegisteredAtMetadataKey 是 auth JSON 顶层保留字段，
// 用于记录 credential 首次进入池子的稳定时间。
// 之所以单独使用一个 CLIProxyAPI 自己的键，而不是直接复用 created_at，
// 是为了避免和上游/provider 自带字段或运行时时间语义混淆。
const FirstRegisteredAtMetadataKey = "cli_proxy_first_registered_at"

// ParseFirstRegisteredAtValue 解析首次入池时间字段。
// 当前统一使用 RFC3339Nano 字符串；这里额外兼容 time.Time，
// 便于测试或内存态直接传值。
func ParseFirstRegisteredAtValue(raw any) (time.Time, bool) {
	switch value := raw.(type) {
	case time.Time:
		if value.IsZero() {
			return time.Time{}, false
		}
		return value.UTC(), true
	case string:
		if value == "" {
			return time.Time{}, false
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.IsZero() {
			return time.Time{}, false
		}
		return parsed.UTC(), true
	default:
		return time.Time{}, false
	}
}

// FirstRegisteredAtFromMetadata 从 metadata 中读取稳定首次入池时间。
func FirstRegisteredAtFromMetadata(metadata map[string]any) (time.Time, bool) {
	if metadata == nil {
		return time.Time{}, false
	}
	return ParseFirstRegisteredAtValue(metadata[FirstRegisteredAtMetadataKey])
}

// FirstRegisteredAt 返回 auth 当前可用的首次入池时间：
// 先看稳定 metadata 字段，没有则退回运行时 CreatedAt。
func FirstRegisteredAt(auth *Auth) (time.Time, bool) {
	if auth == nil {
		return time.Time{}, false
	}
	if registeredAt, ok := FirstRegisteredAtFromMetadata(auth.Metadata); ok {
		return registeredAt, true
	}
	if auth.CreatedAt.IsZero() {
		return time.Time{}, false
	}
	return auth.CreatedAt.UTC(), true
}

// EnsureFirstRegisteredAt 确保 auth 带有稳定首次入池时间。
// 当 metadata 已存在该字段时，只做 CreatedAt 对齐；
// 当 metadata 不为空但字段缺失时，会用 fallback 回填；
// 当 metadata 为空时，仅对齐 CreatedAt，不额外创建 metadata，
// 以免把原本不需要落盘的 config/runtime-only auth 意外变成可持久化对象。
func EnsureFirstRegisteredAt(auth *Auth, fallback time.Time) time.Time {
	registeredAt, _ := ensureFirstRegisteredAtWithChanged(auth, fallback)
	return registeredAt
}

func ensureFirstRegisteredAtWithChanged(auth *Auth, fallback time.Time) (time.Time, bool) {
	if auth == nil {
		return time.Time{}, false
	}
	if registeredAt, ok := FirstRegisteredAtFromMetadata(auth.Metadata); ok {
		if auth.CreatedAt.IsZero() || !auth.CreatedAt.Equal(registeredAt) {
			auth.CreatedAt = registeredAt
		}
		return registeredAt, false
	}

	if fallback.IsZero() {
		fallback = auth.CreatedAt
	}
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	} else {
		fallback = fallback.UTC()
	}
	auth.CreatedAt = fallback

	if auth.Metadata == nil {
		return fallback, false
	}
	auth.Metadata[FirstRegisteredAtMetadataKey] = fallback.Format(time.RFC3339Nano)
	return fallback, true
}

func firstRegisteredAtLess(left, right *Auth) bool {
	leftTime, leftOK := FirstRegisteredAt(left)
	rightTime, rightOK := FirstRegisteredAt(right)

	switch {
	case leftOK && rightOK && !leftTime.Equal(rightTime):
		return leftTime.Before(rightTime)
	case leftOK != rightOK:
		return leftOK
	}

	leftID := ""
	if left != nil {
		leftID = left.ID
	}
	rightID := ""
	if right != nil {
		rightID = right.ID
	}
	return leftID < rightID
}

func sortAuthsByFirstRegisteredAt(auths []*Auth) {
	if len(auths) <= 1 {
		return
	}
	sort.Slice(auths, func(i, j int) bool {
		return firstRegisteredAtLess(auths[i], auths[j])
	})
}
