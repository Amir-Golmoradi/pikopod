package sandbox

import (
	"encoding/json"
	"strconv"

	"github.com/pikopod/pikopod/internal/ir"
)

const validateMaxDepth = 8

func isEnforceableSchema(schema *ir.IrSchemaNode) bool {
	return schema != nil
}

func bodyTypeMatches(expected string, value any) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		return isJSONNumber(value)
	case "integer":
		f, ok := asFloat(value)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(*JSONObject)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func isJSONNumber(value any) bool {
	switch value.(type) {
	case json.Number, float64, int, int64:
		return true
	}
	return false
}

func asFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func jsStrictEqual(enumValue, value any) bool {
	switch ev := enumValue.(type) {
	case string:
		s, ok := value.(string)
		return ok && s == ev
	case bool:
		b, ok := value.(bool)
		return ok && b == ev
	case nil:
		return value == nil
	default:
		ef, eok := asFloat(enumValue)
		vf, vok := asFloat(value)
		return eok && vok && ef == vf
	}
}

func validateNode(schema *ir.IrSchemaNode, value any, path string, depth int, errors *[]string, named map[string]*ir.IrSchemaNode) {
	if depth > validateMaxDepth {
		return
	}
	for hops := 0; schema.Ref != nil && hops < 10; hops++ {
		target, ok := named[*schema.Ref]
		if !ok {
			return
		}
		schema = target
	}
	label := path
	if label == "" {
		label = "body"
	}
	if value == nil {
		if !schema.Nullable.Value && schema.Type.Value != "null" {
			*errors = append(*errors, label+" must not be null")
		}
		return
	}
	expected := schema.Type.Value
	if expected != "unknown" && !bodyTypeMatches(expected, value) {
		*errors = append(*errors, label+" must be "+expected)
		return
	}

	if schema.EnumValues != nil && len(schema.EnumValues.Value) > 0 {
		matched := false
		for _, e := range schema.EnumValues.Value {
			if jsStrictEqual(e, value) {
				matched = true
				break
			}
		}
		if !matched {
			*errors = append(*errors, label+" is not an allowed value")
		}
	}

	switch expected {
	case "object":
		obj := value.(*JSONObject)
		for i := range schema.Properties {
			prop := &schema.Properties[i]
			childPath := prop.Name
			if path != "" {
				childPath = path + "." + prop.Name
			}
			if serverAssigned(prop, depth) {
				continue
			}
			v, present := obj.Get(prop.Name)
			if !present {
				if prop.Required.Value {
					*errors = append(*errors, childPath+" is required")
				}
				continue
			}
			validateNode(&prop.Schema, v, childPath, depth+1, errors, named)
		}
	case "array":
		if schema.Items != nil {
			arr := value.([]any)
			for i := 0; i < len(arr) && i < 1000; i++ {
				validateNode(schema.Items, arr[i], path+"["+strconv.Itoa(i)+"]", depth+1, errors, named)
			}
		}
	}
}

func serverAssigned(prop *ir.PropertySchema, depth int) bool {
	if v, ok := constraintValue(&prop.Schema, "readOnly"); ok {
		if b, isBool := v.(bool); isBool && b {
			return true
		}
	}
	return depth == 0 && prop.Name == "id"
}

func validateBody(schema *ir.IrSchemaNode, value any) []string {
	return validateBodyWith(schema, value, nil)
}

func validateBodyWith(schema *ir.IrSchemaNode, value any, named map[string]*ir.IrSchemaNode) []string {
	if !isEnforceableSchema(schema) {
		return nil
	}
	var errors []string
	validateNode(schema, value, "", 0, &errors, named)
	return errors
}
