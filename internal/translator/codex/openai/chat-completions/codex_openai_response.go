// Package openai provides response translation functionality for Codex to OpenAI API compatibility.
// This package handles the conversion of Codex API responses into OpenAI Chat Completions-compatible
// JSON format, transforming streaming events and non-streaming responses into the format
// expected by OpenAI API clients. It supports both streaming and non-streaming modes,
// handling text content, tool calls, reasoning content, and usage metadata appropriately.
package chat_completions

import (
	"bytes"
	"context"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	dataTag = []byte("data:")
)

// ConvertCliToOpenAIParams holds parameters for response conversion.
type ConvertCliToOpenAIParams struct {
	ResponseID                string
	CreatedAt                 int64
	Model                     string
	FunctionCallIndex         int
	HasReceivedArgumentsDelta bool
	HasToolCallAnnounced      bool
	ReverseToolNameMap        map[string]string
	BaseChunkTemplate         string
}

// 保持空 delta 模板，避免工具调用 chunk 带出多余 null 字段，
// 兼容现有 OpenAI 流式断言与历史客户端行为。
const openAIStreamChunkTemplate = `{"id":"","object":"chat.completion.chunk","created":12345,"model":"model","choices":[{"index":0,"delta":{},"finish_reason":null,"native_finish_reason":null}]}`

func (p *ConvertCliToOpenAIParams) ensureBaseChunkTemplate(defaultModel string) {
	template := openAIStreamChunkTemplate
	model := p.Model
	if model == "" {
		model = defaultModel
	}
	if model != "" {
		template, _ = sjson.Set(template, "model", model)
	}
	template, _ = sjson.Set(template, "created", p.CreatedAt)
	template, _ = sjson.Set(template, "id", p.ResponseID)
	p.BaseChunkTemplate = template
}

func (p *ConvertCliToOpenAIParams) reverseToolName(shortName string, originalRequestRawJSON []byte) string {
	if shortName == "" {
		return shortName
	}
	if p.ReverseToolNameMap == nil {
		p.ReverseToolNameMap = buildReverseMapFromOriginalOpenAI(originalRequestRawJSON)
	}
	if original, ok := p.ReverseToolNameMap[shortName]; ok {
		return original
	}
	return shortName
}

func buildFunctionCallChunk(index int, callID, name, arguments string, includeIdentity bool) string {
	template := `{"index":0,"function":{"arguments":""}}`
	if includeIdentity {
		template = `{"index":0,"id":"","type":"function","function":{"name":"","arguments":""}}`
		template, _ = sjson.Set(template, "id", callID)
		template, _ = sjson.Set(template, "function.name", name)
	}
	template, _ = sjson.Set(template, "index", index)
	template, _ = sjson.Set(template, "function.arguments", arguments)
	return template
}

// ConvertCodexResponseToOpenAI translates a single chunk of a streaming response from the
// Codex API format to the OpenAI Chat Completions streaming format.
// It processes various Codex event types and transforms them into OpenAI-compatible JSON responses.
// The function handles text content, tool calls, reasoning content, and usage metadata, outputting
// responses that match the OpenAI API format. It supports incremental updates for streaming responses.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for maintaining state between calls
//
// Returns:
//   - []string: A slice of strings, each containing an OpenAI-compatible JSON response
func ConvertCodexResponseToOpenAI(_ context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []string {
	if *param == nil {
		p := &ConvertCliToOpenAIParams{
			Model:                     modelName,
			CreatedAt:                 0,
			ResponseID:                "",
			FunctionCallIndex:         -1,
			HasReceivedArgumentsDelta: false,
			HasToolCallAnnounced:      false,
		}
		p.ensureBaseChunkTemplate(modelName)
		*param = p
	}
	params := (*param).(*ConvertCliToOpenAIParams)

	if !bytes.HasPrefix(rawJSON, dataTag) {
		return []string{}
	}
	rawJSON = bytes.TrimSpace(rawJSON[5:])

	rootResult := gjson.ParseBytes(rawJSON)

	typeResult := rootResult.Get("type")
	dataType := typeResult.String()
	if dataType == "response.created" {
		params.ResponseID = rootResult.Get("response.id").String()
		params.CreatedAt = rootResult.Get("response.created_at").Int()
		params.Model = rootResult.Get("response.model").String()
		params.ensureBaseChunkTemplate(modelName)
		return []string{}
	}

	template := params.BaseChunkTemplate
	if template == "" {
		params.ensureBaseChunkTemplate(modelName)
		template = params.BaseChunkTemplate
	}

	if modelResult := rootResult.Get("model"); modelResult.Exists() {
		template, _ = sjson.Set(template, "model", modelResult.String())
	}

	// Extract and set usage metadata (token counts).
	if usageResult := rootResult.Get("response.usage"); usageResult.Exists() {
		if outputTokensResult := usageResult.Get("output_tokens"); outputTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens", outputTokensResult.Int())
		}
		if totalTokensResult := usageResult.Get("total_tokens"); totalTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.total_tokens", totalTokensResult.Int())
		}
		if inputTokensResult := usageResult.Get("input_tokens"); inputTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens", inputTokensResult.Int())
		}
		if cachedTokensResult := usageResult.Get("input_tokens_details.cached_tokens"); cachedTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens_details.cached_tokens", cachedTokensResult.Int())
		}
		if reasoningTokensResult := usageResult.Get("output_tokens_details.reasoning_tokens"); reasoningTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens_details.reasoning_tokens", reasoningTokensResult.Int())
		}
	}

	if dataType == "response.reasoning_summary_text.delta" {
		if deltaResult := rootResult.Get("delta"); deltaResult.Exists() {
			template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
			template, _ = sjson.Set(template, "choices.0.delta.reasoning_content", deltaResult.String())
		}
	} else if dataType == "response.reasoning_summary_text.done" {
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.Set(template, "choices.0.delta.reasoning_content", "\n\n")
	} else if dataType == "response.output_text.delta" {
		if deltaResult := rootResult.Get("delta"); deltaResult.Exists() {
			template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
			template, _ = sjson.Set(template, "choices.0.delta.content", deltaResult.String())
		}
	} else if dataType == "response.completed" {
		finishReason := "stop"
		if params.FunctionCallIndex != -1 {
			finishReason = "tool_calls"
		}
		template, _ = sjson.Set(template, "choices.0.finish_reason", finishReason)
		template, _ = sjson.Set(template, "choices.0.native_finish_reason", finishReason)
	} else if dataType == "response.output_item.added" {
		itemResult := rootResult.Get("item")
		if !itemResult.Exists() || itemResult.Get("type").String() != "function_call" {
			return []string{}
		}

		// Increment index for this new function call item.
		params.FunctionCallIndex++
		params.HasReceivedArgumentsDelta = false
		params.HasToolCallAnnounced = true

		// Restore original tool name if it was shortened.
		name := params.reverseToolName(itemResult.Get("name").String(), originalRequestRawJSON)
		functionCallItemTemplate := buildFunctionCallChunk(params.FunctionCallIndex, itemResult.Get("call_id").String(), name, "", true)

		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.SetRaw(template, "choices.0.delta.tool_calls", "["+functionCallItemTemplate+"]")

	} else if dataType == "response.function_call_arguments.delta" {
		params.HasReceivedArgumentsDelta = true

		deltaValue := rootResult.Get("delta").String()
		functionCallItemTemplate := buildFunctionCallChunk(params.FunctionCallIndex, "", "", deltaValue, false)

		template, _ = sjson.SetRaw(template, "choices.0.delta.tool_calls", "["+functionCallItemTemplate+"]")

	} else if dataType == "response.function_call_arguments.done" {
		if params.HasReceivedArgumentsDelta {
			// Arguments were already streamed via delta events; nothing to emit.
			return []string{}
		}

		// Fallback: no delta events were received, emit the full arguments as a single chunk.
		fullArgs := rootResult.Get("arguments").String()
		functionCallItemTemplate := buildFunctionCallChunk(params.FunctionCallIndex, "", "", fullArgs, false)

		template, _ = sjson.SetRaw(template, "choices.0.delta.tool_calls", "["+functionCallItemTemplate+"]")

	} else if dataType == "response.output_item.done" {
		itemResult := rootResult.Get("item")
		if !itemResult.Exists() || itemResult.Get("type").String() != "function_call" {
			return []string{}
		}

		if params.HasToolCallAnnounced {
			// Tool call was already announced via output_item.added; skip emission.
			params.HasToolCallAnnounced = false
			return []string{}
		}

		// Fallback path: model skipped output_item.added, so emit complete tool call now.
		params.FunctionCallIndex++

		// Restore original tool name if it was shortened.
		name := params.reverseToolName(itemResult.Get("name").String(), originalRequestRawJSON)
		functionCallItemTemplate := buildFunctionCallChunk(params.FunctionCallIndex, itemResult.Get("call_id").String(), name, itemResult.Get("arguments").String(), true)
		template, _ = sjson.Set(template, "choices.0.delta.role", "assistant")
		template, _ = sjson.SetRaw(template, "choices.0.delta.tool_calls", "["+functionCallItemTemplate+"]")

	} else {
		return []string{}
	}

	return []string{template}
}

// ConvertCodexResponseToOpenAINonStream converts a non-streaming Codex response to a non-streaming OpenAI response.
// This function processes the complete Codex response and transforms it into a single OpenAI-compatible
// JSON response. It handles message content, tool calls, reasoning content, and usage metadata, combining all
// the information into a single response that matches the OpenAI API format.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the Codex API
//   - param: A pointer to a parameter object for the conversion (unused in current implementation)
//
// Returns:
//   - string: An OpenAI-compatible JSON response containing all message content and metadata
func ConvertCodexResponseToOpenAINonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) string {
	rootResult := gjson.ParseBytes(rawJSON)
	// Verify this is a response.completed event
	if rootResult.Get("type").String() != "response.completed" {
		return ""
	}

	unixTimestamp := time.Now().Unix()

	responseResult := rootResult.Get("response")
	rev := buildReverseMapFromOriginalOpenAI(originalRequestRawJSON)

	template := `{"id":"","object":"chat.completion","created":123456,"model":"model","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":null,"tool_calls":null},"finish_reason":null,"native_finish_reason":null}]}`

	// Extract and set the model version.
	if modelResult := responseResult.Get("model"); modelResult.Exists() {
		template, _ = sjson.Set(template, "model", modelResult.String())
	}

	// Extract and set the creation timestamp.
	if createdAtResult := responseResult.Get("created_at"); createdAtResult.Exists() {
		template, _ = sjson.Set(template, "created", createdAtResult.Int())
	} else {
		template, _ = sjson.Set(template, "created", unixTimestamp)
	}

	// Extract and set the response ID.
	if idResult := responseResult.Get("id"); idResult.Exists() {
		template, _ = sjson.Set(template, "id", idResult.String())
	}

	// Extract and set usage metadata (token counts).
	if usageResult := responseResult.Get("usage"); usageResult.Exists() {
		if outputTokensResult := usageResult.Get("output_tokens"); outputTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens", outputTokensResult.Int())
		}
		if totalTokensResult := usageResult.Get("total_tokens"); totalTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.total_tokens", totalTokensResult.Int())
		}
		if inputTokensResult := usageResult.Get("input_tokens"); inputTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens", inputTokensResult.Int())
		}
		if cachedTokensResult := usageResult.Get("input_tokens_details.cached_tokens"); cachedTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.prompt_tokens_details.cached_tokens", cachedTokensResult.Int())
		}
		if reasoningTokensResult := usageResult.Get("output_tokens_details.reasoning_tokens"); reasoningTokensResult.Exists() {
			template, _ = sjson.Set(template, "usage.completion_tokens_details.reasoning_tokens", reasoningTokensResult.Int())
		}
	}

	// Process the output array for content and function calls
	outputResult := responseResult.Get("output")
	if outputResult.IsArray() {
		outputArray := outputResult.Array()
		var contentText string
		var reasoningText string
		var toolCalls []string

		for _, outputItem := range outputArray {
			outputType := outputItem.Get("type").String()

			switch outputType {
			case "reasoning":
				// Extract reasoning content from summary
				if summaryResult := outputItem.Get("summary"); summaryResult.IsArray() {
					summaryArray := summaryResult.Array()
					for _, summaryItem := range summaryArray {
						if summaryItem.Get("type").String() == "summary_text" {
							reasoningText = summaryItem.Get("text").String()
							break
						}
					}
				}
			case "message":
				// Extract message content
				if contentResult := outputItem.Get("content"); contentResult.IsArray() {
					contentArray := contentResult.Array()
					for _, contentItem := range contentArray {
						if contentItem.Get("type").String() == "output_text" {
							contentText = contentItem.Get("text").String()
							break
						}
					}
				}
			case "function_call":
				// Handle function call content
				functionCallTemplate := `{"id": "","type": "function","function": {"name": "","arguments": ""}}`

				if callIdResult := outputItem.Get("call_id"); callIdResult.Exists() {
					functionCallTemplate, _ = sjson.Set(functionCallTemplate, "id", callIdResult.String())
				}

				if nameResult := outputItem.Get("name"); nameResult.Exists() {
					n := nameResult.String()
					if orig, ok := rev[n]; ok {
						n = orig
					}
					functionCallTemplate, _ = sjson.Set(functionCallTemplate, "function.name", n)
				}

				if argsResult := outputItem.Get("arguments"); argsResult.Exists() {
					functionCallTemplate, _ = sjson.Set(functionCallTemplate, "function.arguments", argsResult.String())
				}

				toolCalls = append(toolCalls, functionCallTemplate)
			}
		}

		// Set content and reasoning content if found
		if contentText != "" {
			template, _ = sjson.Set(template, "choices.0.message.content", contentText)
			template, _ = sjson.Set(template, "choices.0.message.role", "assistant")
		}

		if reasoningText != "" {
			template, _ = sjson.Set(template, "choices.0.message.reasoning_content", reasoningText)
			template, _ = sjson.Set(template, "choices.0.message.role", "assistant")
		}

		// Add tool calls if any
		if len(toolCalls) > 0 {
			template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls", `[]`)
			for _, toolCall := range toolCalls {
				template, _ = sjson.SetRaw(template, "choices.0.message.tool_calls.-1", toolCall)
			}
			template, _ = sjson.Set(template, "choices.0.message.role", "assistant")
		}
	}

	// Extract and set the finish reason based on status
	if statusResult := responseResult.Get("status"); statusResult.Exists() {
		status := statusResult.String()
		if status == "completed" {
			template, _ = sjson.Set(template, "choices.0.finish_reason", "stop")
			template, _ = sjson.Set(template, "choices.0.native_finish_reason", "stop")
		}
	}

	return template
}

// buildReverseMapFromOriginalOpenAI builds a map of shortened tool name -> original tool name
// from the original OpenAI-style request JSON using the same shortening logic.
func buildReverseMapFromOriginalOpenAI(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	rev := map[string]string{}
	if tools.IsArray() && len(tools.Array()) > 0 {
		var names []string
		arr := tools.Array()
		for i := 0; i < len(arr); i++ {
			t := arr[i]
			if t.Get("type").String() != "function" {
				continue
			}
			fn := t.Get("function")
			if !fn.Exists() {
				continue
			}
			if v := fn.Get("name"); v.Exists() {
				names = append(names, v.String())
			}
		}
		if len(names) > 0 {
			m := buildShortNameMap(names)
			for orig, short := range m {
				rev[short] = orig
			}
		}
	}
	return rev
}
