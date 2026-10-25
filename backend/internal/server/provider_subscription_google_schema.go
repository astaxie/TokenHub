package server

// Schema projection adapted from github.com/yetone/magpie
// internal/gateway/codeassist.go at d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1.
//
// MIT License
//
// Copyright (c) 2026 yetone
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

import "strings"

func prepareAntigravitySubscriptionTools(model string, payload map[string]any) error {
	tools, _ := payload["tools"].([]map[string]any)
	for _, tool := range tools {
		declarations, _ := tool["functionDeclarations"].([]map[string]any)
		for _, declaration := range declarations {
			schema := declaration["parametersJsonSchema"]
			if schema == nil {
				schema = declaration["parameters"]
			}
			projected, err := antigravitySubscriptionSchema(schema)
			if err != nil {
				return err
			}
			declaration["parameters"] = projected
			delete(declaration, "parametersJsonSchema")
		}
	}
	config, _ := payload["toolConfig"].(map[string]any)
	calling, _ := config["functionCallingConfig"].(map[string]any)
	if strings.HasPrefix(model, "claude") && calling["mode"] == "AUTO" {
		calling["mode"] = "VALIDATED"
	}
	return nil
}

// Antigravity only accepts its OpenAPI subset, including for Gemini models.
// Resolve definitions and project nullable unions while preserving property names.
func antigravitySubscriptionSchema(value any) (any, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, NewHTTPError(400, "antigravity_tool_schema_unsupported", "Antigravity tool parameters must be an object schema")
	}
	remaining := 4096
	active := map[string]bool{}
	var projectionErr error
	fail := func(message string) any {
		if projectionErr == nil {
			projectionErr = NewHTTPError(400, "antigravity_tool_schema_unsupported", message)
		}
		return nil
	}
	var walk func(v any, depth int) any
	walk = func(v any, depth int) any {
		if projectionErr != nil {
			return nil
		}
		remaining--
		if remaining < 0 || depth > 32 {
			return fail("Antigravity tool schema expansion exceeds its size or depth limit")
		}
		switch x := v.(type) {
		case []any:
			return fail("Antigravity does not support tuple schemas")
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				if !strings.HasPrefix(ref, "#/") || active[ref] {
					return fail("Antigravity does not support external or recursive schema references")
				}
				var target any = root
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					object, _ := target.(map[string]any)
					name := strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
					target = object[name]
					if target == nil {
						return fail("Antigravity tool schema contains an unresolved reference")
					}
				}
				active[ref] = true
				resolved := walk(target, depth+1)
				delete(active, ref)
				return resolved
			}
			// allOf: one schema with all the members' fields
			if all, ok := x["allOf"].([]any); ok {
				merged := map[string]any{}
				for k, v := range x {
					if k != "allOf" {
						merged[k] = v
					}
				}
				for _, e := range all {
					if m, ok := walk(e, depth+1).(map[string]any); ok {
						for k, v := range m {
							if k == "properties" {
								current, _ := merged["properties"].(map[string]any)
								props := cloneAnyMap(current)
								members, _ := v.(map[string]any)
								for pk, pv := range members {
									props[pk] = pv
								}
								merged["properties"] = props
							} else if k == "required" {
								current, _ := merged[k].([]any)
								required := append([]any{}, current...)
								seen := make(map[string]bool, len(required))
								for _, value := range required {
									if name, ok := value.(string); ok {
										seen[name] = true
									}
								}
								names, _ := v.([]any)
								for _, value := range names {
									if name, ok := value.(string); ok && !seen[name] {
										required = append(required, name)
										seen[name] = true
									}
								}
								merged[k] = required
							} else if _, has := merged[k]; !has {
								merged[k] = v
							}
						}
					}
				}
				return walk(merged, depth+1)
			}
			// anyOf / oneOf: the first member that isn't null, nullable
			for _, k := range []string{"anyOf", "oneOf"} {
				if alts, ok := x[k].([]any); ok {
					var pick map[string]any
					nullable := false
					for _, a := range alts {
						m, ok := a.(map[string]any)
						if !ok {
							return fail("Antigravity tool schema union members must be objects")
						}
						if m["type"] == "null" {
							nullable = true
						} else if pick == nil && m != nil {
							pick = m
						}
					}
					out := map[string]any{}
					if pick != nil {
						if m, ok := walk(pick, depth+1).(map[string]any); ok {
							out = m
						}
					}
					if d, ok := x["description"].(string); ok && out["description"] == nil {
						out["description"] = d
					}
					if nullable {
						out["nullable"] = true
					}
					if out["type"] == nil {
						out["type"] = "string"
					}
					return out
				}
			}
			out := map[string]any{}
			for k, v := range x {
				switch k {
				case "description", "nullable", "required", "pattern", "minimum", "maximum", "minLength", "maxLength",
					"minItems", "maxItems", "minProperties", "maxProperties", "propertyOrdering":
					out[k] = v
				case "exclusiveMinimum", "exclusiveMaximum":
					// a bound it takes only as inclusive (draft 6 on: a number)
					if n, ok := v.(float64); ok {
						if b := "m" + strings.TrimPrefix(k, "exclusiveM"); x[b] == nil {
							out[b] = n
						}
					}
				case "type":
					if ts, ok := v.([]any); ok {
						for _, t := range ts {
							if t == "null" {
								out["nullable"] = true
							} else if out["type"] == nil {
								out["type"] = t
							}
						}
					} else {
						out["type"] = v
					}
				case "properties":
					props := map[string]any{}
					m, ok := v.(map[string]any)
					if !ok {
						return fail("Antigravity tool schema properties must be an object")
					}
					for pk, pv := range m {
						props[pk] = walk(pv, depth+1)
					}
					out[k] = props
				case "items":
					out[k] = walk(v, depth+1)
				case "enum":
					// Google takes string enums only
					if es, ok := v.([]any); ok {
						strs := make([]any, 0, len(es))
						for _, e := range es {
							if s, ok := e.(string); ok {
								strs = append(strs, s)
							}
						}
						if len(strs) == len(es) {
							out[k] = strs
						}
					}
				default:
					// a keyword outside its subset (format, default, title,
					// additionalProperties, const…): Antigravity refuses the
					// request over any it doesn't know (#187)
				}
			}
			if c, ok := x["const"]; ok {
				if s, ok := c.(string); ok {
					out["enum"] = []any{s}
				}
			}
			if out["type"] == nil {
				switch {
				case out["properties"] != nil:
					out["type"] = "object"
				case out["items"] != nil:
					out["type"] = "array"
				}
			}
			if req, ok := out["required"].([]any); ok {
				// required names only the properties there are
				props, _ := out["properties"].(map[string]any)
				keep := []any{}
				for _, n := range req {
					if s, ok := n.(string); ok && props[s] != nil {
						keep = append(keep, s)
					}
				}
				if len(keep) == 0 {
					delete(out, "required")
				} else {
					out["required"] = keep
				}
			}
			return out
		}
		return fail("Antigravity tool schema nodes must be objects")
	}
	result := walk(root, 0)
	return result, projectionErr
}
