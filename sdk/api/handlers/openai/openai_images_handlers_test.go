package openai

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
)

func TestBuildImagesResponsesRequestIncludesPromptImagesAndTool(t *testing.T) {
	tool := []byte(`{"type":"image_generation","action":"edit","model":"gpt-image-2"}`)
	req := buildImagesResponsesRequest("draw a cat", []string{
		"https://example.com/1.png",
		"",
		"https://example.com/2.png",
	}, tool)

	if got := gjson.GetBytes(req, "model").String(); got != defaultImagesMainModel {
		t.Fatalf("model = %q, want %q", got, defaultImagesMainModel)
	}
	if got := gjson.GetBytes(req, "tool_choice.type").String(); got != "image_generation" {
		t.Fatalf("tool_choice.type = %q, want %q", got, "image_generation")
	}
	if got := gjson.GetBytes(req, "input.0.content.0.text").String(); got != "draw a cat" {
		t.Fatalf("prompt = %q, want %q", got, "draw a cat")
	}
	if got := gjson.GetBytes(req, "input.0.content.#").Int(); got != 3 {
		t.Fatalf("content count = %d, want %d", got, 3)
	}
	if got := gjson.GetBytes(req, "tools.0.model").String(); got != defaultImagesToolModel {
		t.Fatalf("tools.0.model = %q, want %q", got, defaultImagesToolModel)
	}
}

func TestCollectImagesFromResponsesStreamBuildsImageResponse(t *testing.T) {
	data := make(chan []byte, 1)
	errs := make(chan *interfaces.ErrorMessage)
	close(errs)

	data <- []byte("data: " + `{"type":"response.completed","response":{"created_at":123,"output":[{"type":"image_generation_call","result":"QUJD","revised_prompt":"reworded","output_format":"png","size":"1024x1024","background":"transparent","quality":"high"}],"tool_usage":{"image_gen":{"images":1}}}}` + "\n\n")
	close(data)

	out, errMsg := collectImagesFromResponsesStream(context.Background(), data, errs, "b64_json")
	if errMsg != nil {
		t.Fatalf("collectImagesFromResponsesStream() error = %v", errMsg)
	}

	if got := gjson.GetBytes(out, "created").Int(); got != 123 {
		t.Fatalf("created = %d, want %d", got, 123)
	}
	if got := gjson.GetBytes(out, "data.0.b64_json").String(); got != "QUJD" {
		t.Fatalf("data.0.b64_json = %q, want %q", got, "QUJD")
	}
	if got := gjson.GetBytes(out, "data.0.revised_prompt").String(); got != "reworded" {
		t.Fatalf("data.0.revised_prompt = %q, want %q", got, "reworded")
	}
	if got := gjson.GetBytes(out, "background").String(); got != "transparent" {
		t.Fatalf("background = %q, want %q", got, "transparent")
	}
	if got := gjson.GetBytes(out, "usage.images").Int(); got != 1 {
		t.Fatalf("usage.images = %d, want %d", got, 1)
	}
}

func TestBuildImagesAPIResponseSupportsURLFormat(t *testing.T) {
	out, err := buildImagesAPIResponse([]imageCallResult{{
		Result:        "QUJD",
		RevisedPrompt: "reworded",
		OutputFormat:  "webp",
	}}, 321, nil, imageCallResult{OutputFormat: "webp"}, "url")
	if err != nil {
		t.Fatalf("buildImagesAPIResponse() error = %v", err)
	}

	if got := gjson.GetBytes(out, "data.0.url").String(); got != "data:image/webp;base64,QUJD" {
		t.Fatalf("data.0.url = %q, want %q", got, "data:image/webp;base64,QUJD")
	}
}

func TestInitialImagesStreamDisconnectError(t *testing.T) {
	errMsg := initialImagesStreamDisconnectError()
	if errMsg == nil {
		t.Fatalf("initialImagesStreamDisconnectError() = nil")
	}
	if errMsg.StatusCode != 502 {
		t.Fatalf("status = %d, want %d", errMsg.StatusCode, 502)
	}
	if errMsg.Error == nil || errMsg.Error.Error() != "stream disconnected before first event" {
		t.Fatalf("error = %v, want %q", errMsg.Error, "stream disconnected before first event")
	}
}
