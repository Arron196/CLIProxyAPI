package claude

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/encoding/protowire"
)

const maxBypassSignatureLen = 32 * 1024 * 1024

// StripInvalidSignatureThinkingBlocks removes Claude thinking blocks whose
// signatures are empty or not in Claude's expected envelope format.
// Valid Claude signatures start with 'E' or 'R' after stripping any optional
// cache/model-group prefix like "claude#".
func StripInvalidSignatureThinkingBlocks(payload []byte) []byte {
	return StripEmptySignatureThinkingBlocks(payload)
}

// StripEmptySignatureThinkingBlocks is the upstream-compatible name for the
// invalid-signature stripping pass used before Antigravity request translation.
func StripEmptySignatureThinkingBlocks(payload []byte) []byte {
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return payload
	}

	modified := false
	for i, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.IsArray() {
			continue
		}

		var kept []string
		stripped := false
		for _, part := range content.Array() {
			if part.Get("type").String() == "thinking" && !hasValidClaudeSignature(part.Get("signature").String()) {
				stripped = true
				continue
			}
			kept = append(kept, part.Raw)
		}
		if !stripped {
			continue
		}
		modified = true
		if len(kept) == 0 {
			payload, _ = sjson.SetRawBytes(payload, fmt.Sprintf("messages.%d.content", i), []byte("[]"))
			continue
		}
		payload, _ = sjson.SetRawBytes(payload, fmt.Sprintf("messages.%d.content", i), []byte("["+strings.Join(kept, ",")+"]"))
	}

	if !modified {
		return payload
	}
	return payload
}

func hasValidClaudeSignature(sig string) bool {
	sig = stripClaudeSignaturePrefix(sig)
	if sig == "" {
		return false
	}
	return sig[0] == 'E' || sig[0] == 'R'
}

// ValidateClaudeBypassSignatures validates thinking signatures in bypass mode.
// In strict mode it checks the decoded protobuf tree; otherwise it performs the
// same E/R envelope validation and normalization used by the translator.
func ValidateClaudeBypassSignatures(inputRawJSON []byte) error {
	messages := gjson.GetBytes(inputRawJSON, "messages")
	if !messages.IsArray() {
		return nil
	}

	for i, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.IsArray() {
			continue
		}
		for j, part := range content.Array() {
			if part.Get("type").String() != "thinking" {
				continue
			}
			rawSignature := strings.TrimSpace(part.Get("signature").String())
			if rawSignature == "" {
				return fmt.Errorf("messages[%d].content[%d]: missing thinking signature", i, j)
			}
			if _, err := normalizeClaudeBypassSignature(rawSignature); err != nil {
				return fmt.Errorf("messages[%d].content[%d]: %w", i, j, err)
			}
		}
	}

	return nil
}

func normalizeClaudeBypassSignature(rawSignature string) (string, error) {
	sig := stripClaudeSignaturePrefix(rawSignature)
	if sig == "" {
		return "", fmt.Errorf("empty signature")
	}
	if len(sig) > maxBypassSignatureLen {
		return "", fmt.Errorf("signature exceeds maximum length (%d bytes)", maxBypassSignatureLen)
	}

	switch sig[0] {
	case 'R':
		if err := validateDoubleLayerSignature(sig); err != nil {
			return "", err
		}
		return sig, nil
	case 'E':
		if err := validateSingleLayerSignature(sig); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString([]byte(sig)), nil
	default:
		return "", fmt.Errorf("invalid signature: expected 'E' or 'R' prefix, got %q", string(sig[0]))
	}
}

func stripClaudeSignaturePrefix(raw string) string {
	sig := strings.TrimSpace(raw)
	if idx := strings.IndexByte(sig, '#'); idx >= 0 {
		sig = strings.TrimSpace(sig[idx+1:])
	}
	return sig
}

func validateDoubleLayerSignature(sig string) error {
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid double-layer signature: base64 decode failed: %w", err)
	}
	if len(decoded) == 0 {
		return fmt.Errorf("invalid double-layer signature: empty after decode")
	}
	if decoded[0] != 'E' {
		return fmt.Errorf("invalid double-layer signature: inner does not start with 'E', got 0x%02x", decoded[0])
	}
	return validateSingleLayerSignatureContent(string(decoded))
}

func validateSingleLayerSignature(sig string) error {
	return validateSingleLayerSignatureContent(sig)
}

func validateSingleLayerSignatureContent(sig string) error {
	decoded, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("invalid single-layer signature: base64 decode failed: %w", err)
	}
	if len(decoded) == 0 {
		return fmt.Errorf("invalid single-layer signature: empty after decode")
	}
	if decoded[0] != 0x12 {
		return fmt.Errorf("invalid Claude signature: expected first byte 0x12, got 0x%02x", decoded[0])
	}
	if !cache.SignatureBypassStrictMode() {
		return nil
	}
	return validateClaudeSignatureTree(decoded)
}

func validateClaudeSignatureTree(payload []byte) error {
	container, err := extractBytesField(payload, 2, "top-level protobuf")
	if err != nil {
		return err
	}
	channelBlock, err := extractBytesField(container, 1, "Claude Field 2 container")
	if err != nil {
		return err
	}
	return validateClaudeChannelBlock(channelBlock)
}

func validateClaudeChannelBlock(channelBlock []byte) error {
	haveChannelID := false
	err := walkProtobufFields(channelBlock, func(num protowire.Number, typ protowire.Type, raw []byte) error {
		switch num {
		case 1:
			if typ != protowire.VarintType {
				return fmt.Errorf("invalid Claude signature: Field 2.1.1 channel_id must be varint")
			}
			if _, err := decodeVarintField(raw, "Field 2.1.1 channel_id"); err != nil {
				return err
			}
			haveChannelID = true
		case 2, 3, 7:
			if typ != protowire.VarintType {
				return fmt.Errorf("invalid Claude signature: Field 2.1.%d must be varint", num)
			}
			_, err := decodeVarintField(raw, fmt.Sprintf("Field 2.1.%d", num))
			return err
		case 5, 6:
			if typ != protowire.BytesType {
				return fmt.Errorf("invalid Claude signature: Field 2.1.%d must be bytes", num)
			}
			_, err := decodeBytesField(raw, fmt.Sprintf("Field 2.1.%d", num))
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !haveChannelID {
		return fmt.Errorf("invalid Claude signature: missing Field 2.1.1 channel_id")
	}
	return nil
}

func extractBytesField(msg []byte, fieldNum protowire.Number, scope string) ([]byte, error) {
	var value []byte
	err := walkProtobufFields(msg, func(num protowire.Number, typ protowire.Type, raw []byte) error {
		if num != fieldNum {
			return nil
		}
		if typ != protowire.BytesType {
			return fmt.Errorf("invalid Claude signature: %s field %d must be bytes", scope, fieldNum)
		}
		bytesValue, err := decodeBytesField(raw, fmt.Sprintf("%s field %d", scope, fieldNum))
		if err != nil {
			return err
		}
		value = bytesValue
		return nil
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("invalid Claude signature: missing %s field %d", scope, fieldNum)
	}
	return value, nil
}

func walkProtobufFields(msg []byte, visit func(num protowire.Number, typ protowire.Type, raw []byte) error) error {
	for offset := 0; offset < len(msg); {
		num, typ, n := protowire.ConsumeTag(msg[offset:])
		if n < 0 {
			return fmt.Errorf("invalid Claude signature: malformed protobuf tag: %w", protowire.ParseError(n))
		}
		offset += n
		valueLen := protowire.ConsumeFieldValue(num, typ, msg[offset:])
		if valueLen < 0 {
			return fmt.Errorf("invalid Claude signature: malformed protobuf field %d: %w", num, protowire.ParseError(valueLen))
		}
		fieldRaw := msg[offset : offset+valueLen]
		if err := visit(num, typ, fieldRaw); err != nil {
			return err
		}
		offset += valueLen
	}
	return nil
}

func decodeVarintField(raw []byte, label string) (uint64, error) {
	value, n := protowire.ConsumeVarint(raw)
	if n < 0 {
		return 0, fmt.Errorf("invalid Claude signature: failed to decode %s: %w", label, protowire.ParseError(n))
	}
	return value, nil
}

func decodeBytesField(raw []byte, label string) ([]byte, error) {
	value, n := protowire.ConsumeBytes(raw)
	if n < 0 {
		return nil, fmt.Errorf("invalid Claude signature: failed to decode %s: %w", label, protowire.ParseError(n))
	}
	return value, nil
}
