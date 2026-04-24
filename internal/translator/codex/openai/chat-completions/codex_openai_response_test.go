package chat_completions

import (
	"context"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToOpenAI_StreamSetsModelFromResponseCreated(t *testing.T) {
	ctx := context.Background()
	var param any

	modelName := "gpt-5.3-codex"

	out := ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.created","response":{"id":"resp_123","created_at":1700000000,"model":"gpt-5.3-codex"}}`), &param)
	if len(out) != 0 {
		t.Fatalf("expected no output for response.created, got %d chunks", len(out))
	}

	out = ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.output_text.delta","delta":"hello"}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotModel := gjson.Get(out[0], "model").String()
	if gotModel != modelName {
		t.Fatalf("expected model %q, got %q", modelName, gotModel)
	}
}

func TestConvertCodexResponseToOpenAI_FirstChunkUsesRequestModelName(t *testing.T) {
	ctx := context.Background()
	var param any

	modelName := "gpt-5.3-codex"

	out := ConvertCodexResponseToOpenAI(ctx, modelName, nil, nil, []byte(`data: {"type":"response.output_text.delta","delta":"hello"}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotModel := gjson.Get(out[0], "model").String()
	if gotModel != modelName {
		t.Fatalf("expected model %q, got %q", modelName, gotModel)
	}
}

func TestConvertCodexResponseToOpenAI_ToolCallChunkOmitsNullContentFields(t *testing.T) {
	ctx := context.Background()
	var param any

	out := ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_123","name":"websearch"}}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	if gjson.Get(out[0], "choices.0.delta.content").Exists() {
		t.Fatalf("expected content to be omitted, got %s", out[0])
	}
	if gjson.Get(out[0], "choices.0.delta.reasoning_content").Exists() {
		t.Fatalf("expected reasoning_content to be omitted, got %s", out[0])
	}
	if !gjson.Get(out[0], "choices.0.delta.tool_calls").Exists() {
		t.Fatalf("expected tool_calls to exist, got %s", out[0])
	}
}

func TestConvertCodexResponseToOpenAI_ToolCallArgumentsDeltaOmitsNullContentFields(t *testing.T) {
	ctx := context.Background()
	var param any

	out := ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_123","name":"websearch"}}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected tool call announcement chunk, got %d", len(out))
	}

	out = ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, []byte(`data: {"type":"response.function_call_arguments.delta","delta":"{\"query\":\"OpenAI\"}"}`), &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	if gjson.Get(out[0], "choices.0.delta.content").Exists() {
		t.Fatalf("expected content to be omitted, got %s", out[0])
	}
	if gjson.Get(out[0], "choices.0.delta.reasoning_content").Exists() {
		t.Fatalf("expected reasoning_content to be omitted, got %s", out[0])
	}
	if !gjson.Get(out[0], "choices.0.delta.tool_calls.0.function.arguments").Exists() {
		t.Fatalf("expected tool call arguments delta to exist, got %s", out[0])
	}
}

func TestConvertCodexResponseToOpenAINonStream_UsesToolCallsFinishReason(t *testing.T) {
	out := ConvertCodexResponseToOpenAINonStream(
		context.Background(),
		"gpt-5.4",
		nil,
		nil,
		[]byte(`{
			"type":"response.completed",
			"response":{
				"id":"resp_123",
				"created_at":1700000000,
				"model":"gpt-5.4",
				"status":"completed",
				"output":[
					{
						"type":"function_call",
						"call_id":"call_1",
						"name":"lookup_weather",
						"arguments":"{\"city\":\"Hong Kong\"}"
					}
				]
			}
		}`),
		nil,
	)

	if out == "" {
		t.Fatal("expected non-stream output")
	}
	if got := gjson.Get(out, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want %q. output=%s", got, "tool_calls", out)
	}
	if got := gjson.Get(out, "choices.0.native_finish_reason").String(); got != "tool_calls" {
		t.Fatalf("native_finish_reason = %q, want %q. output=%s", got, "tool_calls", out)
	}
}

func TestConvertCodexResponseToOpenAI_StreamPartialImageEmitsDeltaImages(t *testing.T) {
	ctx := context.Background()
	var param any

	chunk := []byte(`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_123","output_format":"png","partial_image_b64":"aGVsbG8=","partial_image_index":0}`)
	out := ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, chunk, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotURL := gjson.Get(out[0], "choices.0.delta.images.0.image_url.url").String()
	if gotURL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("expected image url %q, got %q; chunk=%s", "data:image/png;base64,aGVsbG8=", gotURL, out[0])
	}

	out = ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, chunk, &param)
	if len(out) != 0 {
		t.Fatalf("expected duplicate image chunk to be suppressed, got %d", len(out))
	}
}

func TestConvertCodexResponseToOpenAI_StreamImageGenerationDoneEmitsOnlyNewImage(t *testing.T) {
	ctx := context.Background()
	var param any

	partial := []byte(`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_123","output_format":"png","partial_image_b64":"aGVsbG8=","partial_image_index":0}`)
	out := ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, partial, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	doneSame := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","output_format":"png","result":"aGVsbG8="}}`)
	out = ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, doneSame, &param)
	if len(out) != 0 {
		t.Fatalf("expected identical final image to be suppressed, got %d", len(out))
	}

	doneNew := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","output_format":"jpeg","result":"Ymll"}}`)
	out = ConvertCodexResponseToOpenAI(ctx, "gpt-5.4", nil, nil, doneNew, &param)
	if len(out) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(out))
	}

	gotURL := gjson.Get(out[0], "choices.0.delta.images.0.image_url.url").String()
	if gotURL != "data:image/jpeg;base64,Ymll" {
		t.Fatalf("expected image url %q, got %q; chunk=%s", "data:image/jpeg;base64,Ymll", gotURL, out[0])
	}
}

func TestConvertCodexResponseToOpenAI_NonStreamIncludesMessageImages(t *testing.T) {
	raw := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_123",
			"created_at":1700000000,
			"model":"gpt-5.4",
			"status":"completed",
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},
			"output":[
				{"type":"message","content":[{"type":"output_text","text":"ok"}]},
				{"type":"image_generation_call","output_format":"png","result":"aGVsbG8="}
			]
		}
	}`)

	out := ConvertCodexResponseToOpenAINonStream(context.Background(), "gpt-5.4", nil, nil, raw, nil)
	if out == "" {
		t.Fatal("expected non-stream output")
	}
	gotURL := gjson.Get(out, "choices.0.message.images.0.image_url.url").String()
	if gotURL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("expected image url %q, got %q; output=%s", "data:image/png;base64,aGVsbG8=", gotURL, out)
	}
}
