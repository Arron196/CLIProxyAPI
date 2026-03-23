package chat_completions

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// convertOpenAIRequestToCodexLegacyImpl is the original gjson/sjson-based implementation,
// preserved for equivalence testing against the new Unmarshal/Marshal implementation.
func convertOpenAIRequestToCodexLegacyImpl(modelName string, inputRawJSON []byte, stream bool) []byte {
	rawJSON := inputRawJSON
	out := `{"instructions":""}`

	out, _ = sjson.Set(out, "stream", stream)

	if v := gjson.GetBytes(rawJSON, "reasoning_effort"); v.Exists() {
		out, _ = sjson.Set(out, "reasoning.effort", v.Value())
	} else {
		out, _ = sjson.Set(out, "reasoning.effort", "medium")
	}
	out, _ = sjson.Set(out, "parallel_tool_calls", true)
	out, _ = sjson.Set(out, "reasoning.summary", "auto")
	out, _ = sjson.Set(out, "include", []string{"reasoning.encrypted_content"})

	out, _ = sjson.Set(out, "model", modelName)

	originalToolNameMap := map[string]string{}
	{
		tools := gjson.GetBytes(rawJSON, "tools")
		if tools.IsArray() && len(tools.Array()) > 0 {
			var names []string
			arr := tools.Array()
			for i := 0; i < len(arr); i++ {
				t := arr[i]
				if t.Get("type").String() == "function" {
					fn := t.Get("function")
					if fn.Exists() {
						if v := fn.Get("name"); v.Exists() {
							names = append(names, v.String())
						}
					}
				}
			}
			if len(names) > 0 {
				originalToolNameMap = buildShortNameMap(names)
			}
		}
	}

	messages := gjson.GetBytes(rawJSON, "messages")

	out, _ = sjson.SetRaw(out, "input", `[]`)
	if messages.IsArray() {
		arr := messages.Array()
		for i := 0; i < len(arr); i++ {
			m := arr[i]
			role := m.Get("role").String()

			switch role {
			case "tool":
				toolCallID := m.Get("tool_call_id").String()
				content := m.Get("content").String()

				funcOutput := `{}`
				funcOutput, _ = sjson.Set(funcOutput, "type", "function_call_output")
				funcOutput, _ = sjson.Set(funcOutput, "call_id", toolCallID)
				funcOutput, _ = sjson.Set(funcOutput, "output", content)
				out, _ = sjson.SetRaw(out, "input.-1", funcOutput)

			default:
				msg := `{}`
				msg, _ = sjson.Set(msg, "type", "message")
				if role == "system" {
					msg, _ = sjson.Set(msg, "role", "developer")
				} else {
					msg, _ = sjson.Set(msg, "role", role)
				}

				msg, _ = sjson.SetRaw(msg, "content", `[]`)

				c := m.Get("content")
				if c.Exists() && c.Type == gjson.String && c.String() != "" {
					partType := "input_text"
					if role == "assistant" {
						partType = "output_text"
					}
					part := `{}`
					part, _ = sjson.Set(part, "type", partType)
					part, _ = sjson.Set(part, "text", c.String())
					msg, _ = sjson.SetRaw(msg, "content.-1", part)
				} else if c.Exists() && c.IsArray() {
					items := c.Array()
					for j := 0; j < len(items); j++ {
						it := items[j]
						t := it.Get("type").String()
						switch t {
						case "text":
							partType := "input_text"
							if role == "assistant" {
								partType = "output_text"
							}
							part := `{}`
							part, _ = sjson.Set(part, "type", partType)
							part, _ = sjson.Set(part, "text", it.Get("text").String())
							msg, _ = sjson.SetRaw(msg, "content.-1", part)
						case "image_url":
							if role == "user" {
								part := `{}`
								part, _ = sjson.Set(part, "type", "input_image")
								if u := it.Get("image_url.url"); u.Exists() {
									part, _ = sjson.Set(part, "image_url", u.String())
								}
								msg, _ = sjson.SetRaw(msg, "content.-1", part)
							}
						case "file":
							if role == "user" {
								fileData := it.Get("file.file_data").String()
								filename := it.Get("file.filename").String()
								if fileData != "" {
									part := `{}`
									part, _ = sjson.Set(part, "type", "input_file")
									part, _ = sjson.Set(part, "file_data", fileData)
									if filename != "" {
										part, _ = sjson.Set(part, "filename", filename)
									}
									msg, _ = sjson.SetRaw(msg, "content.-1", part)
								}
							}
						}
					}
				}

				// 兼容现有工具调用语义：assistant 只有 tool_calls 且无文本时，
				// 不能额外落一个空 message，否则 call_id 配对会错位。
				if role != "assistant" || len(gjson.Get(msg, "content").Array()) > 0 {
					out, _ = sjson.SetRaw(out, "input.-1", msg)
				}

				if role == "assistant" {
					toolCalls := m.Get("tool_calls")
					if toolCalls.Exists() && toolCalls.IsArray() {
						toolCallsArr := toolCalls.Array()
						for j := 0; j < len(toolCallsArr); j++ {
							tc := toolCallsArr[j]
							if tc.Get("type").String() == "function" {
								funcCall := `{}`
								funcCall, _ = sjson.Set(funcCall, "type", "function_call")
								funcCall, _ = sjson.Set(funcCall, "call_id", tc.Get("id").String())
								{
									name := tc.Get("function.name").String()
									if short, ok := originalToolNameMap[name]; ok {
										name = short
									} else {
										name = shortenNameIfNeeded(name)
									}
									funcCall, _ = sjson.Set(funcCall, "name", name)
								}
								funcCall, _ = sjson.Set(funcCall, "arguments", tc.Get("function.arguments").String())
								out, _ = sjson.SetRaw(out, "input.-1", funcCall)
							}
						}
					}
				}
			}
		}
	}

	rf := gjson.GetBytes(rawJSON, "response_format")
	text := gjson.GetBytes(rawJSON, "text")
	if rf.Exists() {
		if !gjson.Get(out, "text").Exists() {
			out, _ = sjson.SetRaw(out, "text", `{}`)
		}

		rft := rf.Get("type").String()
		switch rft {
		case "text":
			out, _ = sjson.Set(out, "text.format.type", "text")
		case "json_schema":
			js := rf.Get("json_schema")
			if js.Exists() {
				out, _ = sjson.Set(out, "text.format.type", "json_schema")
				if v := js.Get("name"); v.Exists() {
					out, _ = sjson.Set(out, "text.format.name", v.Value())
				}
				if v := js.Get("strict"); v.Exists() {
					out, _ = sjson.Set(out, "text.format.strict", v.Value())
				}
				if v := js.Get("schema"); v.Exists() {
					out, _ = sjson.SetRaw(out, "text.format.schema", v.Raw)
				}
			}
		}

		if text.Exists() {
			if v := text.Get("verbosity"); v.Exists() {
				out, _ = sjson.Set(out, "text.verbosity", v.Value())
			}
		}
	} else if text.Exists() {
		if v := text.Get("verbosity"); v.Exists() {
			if !gjson.Get(out, "text").Exists() {
				out, _ = sjson.SetRaw(out, "text", `{}`)
			}
			out, _ = sjson.Set(out, "text.verbosity", v.Value())
		}
	}

	tools := gjson.GetBytes(rawJSON, "tools")
	if tools.IsArray() && len(tools.Array()) > 0 {
		out, _ = sjson.SetRaw(out, "tools", `[]`)
		arr := tools.Array()
		for i := 0; i < len(arr); i++ {
			t := arr[i]
			toolType := t.Get("type").String()
			if toolType != "" && toolType != "function" && t.IsObject() {
				out, _ = sjson.SetRaw(out, "tools.-1", t.Raw)
				continue
			}

			if toolType == "function" {
				item := `{}`
				item, _ = sjson.Set(item, "type", "function")
				fn := t.Get("function")
				if fn.Exists() {
					if v := fn.Get("name"); v.Exists() {
						name := v.String()
						if short, ok := originalToolNameMap[name]; ok {
							name = short
						} else {
							name = shortenNameIfNeeded(name)
						}
						item, _ = sjson.Set(item, "name", name)
					}
					if v := fn.Get("description"); v.Exists() {
						item, _ = sjson.Set(item, "description", v.Value())
					}
					if v := fn.Get("parameters"); v.Exists() {
						item, _ = sjson.SetRaw(item, "parameters", v.Raw)
					}
					if v := fn.Get("strict"); v.Exists() {
						item, _ = sjson.Set(item, "strict", v.Value())
					}
				}
				out, _ = sjson.SetRaw(out, "tools.-1", item)
			}
		}
	}

	if tc := gjson.GetBytes(rawJSON, "tool_choice"); tc.Exists() {
		switch {
		case tc.Type == gjson.String:
			out, _ = sjson.Set(out, "tool_choice", tc.String())
		case tc.IsObject():
			tcType := tc.Get("type").String()
			if tcType == "function" {
				name := tc.Get("function.name").String()
				if name != "" {
					if short, ok := originalToolNameMap[name]; ok {
						name = short
					} else {
						name = shortenNameIfNeeded(name)
					}
				}
				choice := `{}`
				choice, _ = sjson.Set(choice, "type", "function")
				if name != "" {
					choice, _ = sjson.Set(choice, "name", name)
				}
				out, _ = sjson.SetRaw(out, "tool_choice", choice)
			} else if tcType != "" {
				out, _ = sjson.SetRaw(out, "tool_choice", tc.Raw)
			}
		}
	}

	out, _ = sjson.Set(out, "store", false)
	return []byte(out)
}
