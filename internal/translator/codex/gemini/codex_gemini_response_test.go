package gemini

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToGemini_StreamPartialImageDeduplicates(t *testing.T) {
	ctx := context.Background()
	var param any

	chunk := []byte(`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_123","output_format":"png","partial_image_b64":"aGVsbG8=","partial_image_index":0}`)
	out := ConvertCodexResponseToGemini(ctx, "gemini-2.5-pro", nil, nil, chunk, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}
	if got := gjson.Get(out[0], "candidates.0.content.parts.0.inlineData.mimeType").String(); got != "image/png" {
		t.Fatalf("mimeType = %q, want %q; chunk=%s", got, "image/png", out[0])
	}
	if got := gjson.Get(out[0], "candidates.0.content.parts.0.inlineData.data").String(); got != "aGVsbG8=" {
		t.Fatalf("inlineData.data = %q, want %q; chunk=%s", got, "aGVsbG8=", out[0])
	}

	out = ConvertCodexResponseToGemini(ctx, "gemini-2.5-pro", nil, nil, chunk, &param)
	if len(out) != 0 {
		t.Fatalf("expected duplicate image chunk to be suppressed, got %d", len(out))
	}
}

func TestConvertCodexResponseToGemini_StreamImageGenerationDoneDeduplicatesAgainstPartial(t *testing.T) {
	ctx := context.Background()
	var param any

	partial := []byte(`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_123","output_format":"png","partial_image_b64":"aGVsbG8=","partial_image_index":0}`)
	out := ConvertCodexResponseToGemini(ctx, "gemini-2.5-pro", nil, nil, partial, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	doneSame := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","output_format":"png","result":"aGVsbG8="}}`)
	out = ConvertCodexResponseToGemini(ctx, "gemini-2.5-pro", nil, nil, doneSame, &param)
	if len(out) != 0 {
		t.Fatalf("expected identical final image to be suppressed, got %d", len(out))
	}

	doneNew := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","output_format":"jpeg","result":"Ymll"}}`)
	out = ConvertCodexResponseToGemini(ctx, "gemini-2.5-pro", nil, nil, doneNew, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}
	if got := gjson.Get(out[0], "candidates.0.content.parts.0.inlineData.mimeType").String(); got != "image/jpeg" {
		t.Fatalf("mimeType = %q, want %q; chunk=%s", got, "image/jpeg", out[0])
	}
}

func TestConvertCodexResponseToGemini_NonStreamIncludesImageParts(t *testing.T) {
	raw := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_123",
			"created_at":1700000000,
			"status":"completed",
			"usage":{"input_tokens":1,"output_tokens":1},
			"output":[
				{"type":"message","content":[{"type":"output_text","text":"ok"}]},
				{"type":"image_generation_call","output_format":"webp","result":"aGVsbG8="}
			]
		}
	}`)

	out := ConvertCodexResponseToGeminiNonStream(context.Background(), "gemini-2.5-pro", nil, nil, raw, nil)
	if out == "" {
		t.Fatal("expected non-stream output")
	}
	if got := gjson.Get(out, "candidates.0.content.parts.1.inlineData.mimeType").String(); got != "image/webp" {
		t.Fatalf("mimeType = %q, want %q; output=%s", got, "image/webp", out)
	}
	if got := gjson.Get(out, "candidates.0.content.parts.1.inlineData.data").String(); got != "aGVsbG8=" {
		t.Fatalf("inlineData.data = %q, want %q; output=%s", got, "aGVsbG8=", out)
	}
}
