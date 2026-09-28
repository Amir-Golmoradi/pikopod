package sandbox

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pikopod/pikopod/internal/ir"
)

type synthContext struct {
	prng           *Prng
	virtualClockMs int64
	namedSchemas   map[string]*ir.IrSchemaNode
	status         int
	description    string
	sandbox        string
	trace          func(field, source string)
}

func (ctx *synthContext) forResponse(endpoint *ir.Endpoint, status int) *synthContext {
	ctx.status = status
	ctx.description = ""
	if endpoint == nil {
		return ctx
	}
	if def := errorResponseDef(endpoint, status); def != nil && def.Description != nil {
		ctx.description = strings.TrimSpace(def.Description.Value)
	}
	return ctx
}

func makeContext(seed string, virtualClockMs int64, namedSchemas map[string]*ir.IrSchemaNode) *synthContext {
	return &synthContext{prng: NewPrng(seed), virtualClockMs: virtualClockMs, namedSchemas: namedSchemas, sandbox: "sandbox"}
}

const (
	synthMaxDepth      = 6
	synthMaxArrayItems = 4
)

func numericConstraint(schema *ir.IrSchemaNode, key string) *float64 {
	for _, c := range schema.Constraints {
		if c.Key == key {
			switch v := c.Value.Value.(type) {
			case float64:
				return &v
			case int:
				f := float64(v)
				return &f
			case int64:
				f := float64(v)
				return &f
			case json.Number:
				if f, err := v.Float64(); err == nil {
					return &f
				}
			}
			return nil
		}
	}
	return nil
}

func isoFrom(virtualClockMs int64, dateOnly bool) string {
	iso := time.UnixMilli(virtualClockMs).UTC().Format("2006-01-02T15:04:05.000Z")
	if dateOnly {
		return iso[:10]
	}
	return iso
}

func pickAny(p *Prng, items []any) any {
	return items[p.Int(0, len(items)-1)]
}

var lnameEmail = regexp.MustCompile(`email`)

func synthString(schema *ir.IrSchemaNode, ctx *synthContext, fieldName string) string {
	format := ""
	if schema.Format != nil {
		format = schema.Format.Value
	}
	p := ctx.prng
	if format != "" {
		ctx.note(fieldName, "format "+format)
	}
	switch format {
	case "uuid":
		return p.Hex(8) + "-" + p.Hex(4) + "-4" + p.Hex(3) + "-" + p.Pick([]string{"8", "9", "a", "b"}) + p.Hex(3) + "-" + p.Hex(12)
	case "email":
		return p.Word() + "." + p.Word() + "@" + p.Word() + "." + p.Pick([]string{"com", "io", "test"})
	case "date-time":
		return isoFrom(ctx.virtualClockMs, false)
	case "date":
		return isoFrom(ctx.virtualClockMs, true)
	case "uri", "url":
		return "https://" + ctx.sandbox + ".test/" + p.Word()
	case "hostname":
		return p.Word() + "." + p.Pick([]string{"com", "io", "test"})
	case "ipv4":
		return strconv.Itoa(p.Int(1, 254)) + "." + strconv.Itoa(p.Int(0, 255)) + "." + strconv.Itoa(p.Int(0, 255)) + "." + strconv.Itoa(p.Int(1, 254))
	}

	lname := strings.ToLower(fieldName)
	if lnameEmail.MatchString(lname) {
		ctx.note(fieldName, "convention email")
		return p.Word() + "@" + p.Word() + ".test"
	}
	if value, source, ok := conventionString(ctx, fieldName); ok {
		ctx.note(fieldName, source)
		return value
	}
	ctx.note(fieldName, "fallback")

	min := numericConstraint(schema, "minLength")
	max := numericConstraint(schema, "maxLength")
	s := p.Word()
	if min != nil && float64(len(s)) < *min {
		s = s + p.Token(int(*min)-len(s))
	}
	if max != nil && float64(len(s)) > *max {
		end := int(math.Max(0, *max))
		s = s[:end]
	}
	return s
}

func synthNumber(schema *ir.IrSchemaNode, ctx *synthContext, integer bool, fieldName string) any {
	min := numericConstraint(schema, "minimum")
	max := numericConstraint(schema, "maximum")
	if min == nil && max == nil {
		if value, source, ok := conventionNumber(ctx, fieldName, integer); ok {
			ctx.note(fieldName, source)
			return value
		}
	}
	return synthNumberRange(min, max, ctx, integer)
}

func synthNumberRange(min, max *float64, ctx *synthContext, integer bool) any {
	lo := 0.0
	if min != nil {
		lo = *min
	}
	hi := 1000.0
	if max != nil {
		hi = *max
	} else if min != nil {
		hi = *min + 1000
	}
	if integer {
		return ctx.prng.Int(int(math.Ceil(lo)), int(math.Floor(hi)))
	}

	return math.Floor((lo+ctx.prng.Next()*(hi-lo))*100+0.5) / 100
}

func synthesize(schema *ir.IrSchemaNode, ctx *synthContext, depth int, fieldName string) any {
	if depth > synthMaxDepth {
		return nil
	}

	if schema.Ref != nil {
		if target, ok := ctx.namedSchemas[*schema.Ref]; ok {
			return synthesize(target, ctx, depth+1, fieldName)
		}
		return nil
	}
	if schema.Composition != nil && len(schema.Composition.Members) > 0 {
		return synthesize(flattenComposition(schema, ctx, depth), ctx, depth+1, fieldName)
	}
	if value, source, ok := declaredValue(schema); ok {
		ctx.note(fieldName, source)
		return value
	}
	if schema.EnumValues != nil && len(schema.EnumValues.Value) > 0 {
		ctx.note(fieldName, "enum")
		return pickAny(ctx.prng, schema.EnumValues.Value)
	}

	switch schema.Type.Value {
	case "object":
		out := NewJSONObject()
		for i := range schema.Properties {
			prop := &schema.Properties[i]
			out.Set(prop.Name, synthesize(&prop.Schema, ctx, depth+1, prop.Name))
		}
		return out
	case "array":
		if schema.Items == nil {
			return []any{}
		}
		min := 1.0
		if m := numericConstraint(schema, "minItems"); m != nil {
			min = *m
		}
		max := 3.0
		if m := numericConstraint(schema, "maxItems"); m != nil {
			max = *m
		}
		count := ctx.prng.Int(int(min), int(math.Max(min, max)))
		if count < 0 {
			count = 0
		}
		if count > synthMaxArrayItems {
			count = synthMaxArrayItems
		}
		out := make([]any, count)
		for i := 0; i < count; i++ {
			out[i] = synthesize(schema.Items, ctx, depth+1, fieldName)
		}
		return out
	case "string":
		return synthString(schema, ctx, fieldName)
	case "integer":
		return synthNumber(schema, ctx, true, fieldName)
	case "number":
		return synthNumber(schema, ctx, false, fieldName)
	case "boolean":
		if value, ok := conventionBool(ctx, fieldName); ok {
			ctx.note(fieldName, "convention "+strconv.FormatBool(value)+" on "+strconv.Itoa(ctx.status))
			return value
		}
		return ctx.prng.Bool()
	case "null":
		return nil
	default:

		return ctx.prng.Word()
	}
}

func derefSchema(schema *ir.IrSchemaNode, ctx *synthContext, depth int) *ir.IrSchemaNode {
	if schema == nil || depth > 10 {
		return schema
	}
	if schema.Ref != nil {
		return derefSchema(ctx.namedSchemas[*schema.Ref], ctx, depth+1)
	}
	if schema.Composition != nil && len(schema.Composition.Members) > 0 {
		return derefSchema(flattenComposition(schema, ctx, depth), ctx, depth+1)
	}
	return schema
}

func flattenComposition(schema *ir.IrSchemaNode, ctx *synthContext, depth int) *ir.IrSchemaNode {
	members := schema.Composition.Members
	if schema.Composition.Kind != "allOf" || depth > 10 {
		return &members[0]
	}
	merged := &ir.IrSchemaNode{ID: schema.ID, Type: ir.Prov[ir.ScalarType]{Value: "object"}, SourcePointer: schema.SourcePointer}
	seen := map[string]bool{}
	objects := 0
	for i := range members {
		m := derefSchema(&members[i], ctx, depth+1)
		if m == nil || m.Type.Value != "object" {
			continue
		}
		objects++
		merged.Nullable = m.Nullable
		for _, p := range m.Properties {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			merged.Properties = append(merged.Properties, p)
		}
		merged.Constraints = append(merged.Constraints, m.Constraints...)
	}
	if objects == 0 {
		return &members[0]
	}
	return merged
}

func completeResource(responseSchema *ir.IrSchemaNode, provided *JSONObject, ctx *synthContext, declaredOnly bool) *JSONObject {
	resolved := derefSchema(responseSchema, ctx, 0)
	if resolved == nil || resolved.Type.Value != "object" {
		if declaredOnly {
			return NewJSONObject()
		}
		return provided.Clone()
	}
	base, _ := synthesize(resolved, ctx, 0, "").(*JSONObject)
	if base == nil {
		base = NewJSONObject()
	}
	if !declaredOnly {
		out := base.Clone()
		for _, k := range provided.Keys() {
			v, _ := provided.Get(k)
			out.Set(k, v)
		}
		return out
	}
	declared := map[string]bool{}
	for _, p := range resolved.Properties {
		declared[p.Name] = true
	}
	out := base.Clone()
	for _, k := range provided.Keys() {
		if declared[k] {
			v, _ := provided.Get(k)
			out.Set(k, v)
		}
	}
	return out
}

var countLike = regexp.MustCompile(`(?i)count|total|size`)

func shapeListBody(responseSchema *ir.IrSchemaNode, items []any, ctx *synthContext) any {
	resolved := derefSchema(responseSchema, ctx, 0)
	if resolved == nil || resolved.Type.Value != "object" {
		return items
	}

	var arrayProp *ir.PropertySchema
	for i := range resolved.Properties {
		if resolved.Properties[i].Schema.Type.Value == "array" {
			arrayProp = &resolved.Properties[i]
			break
		}
	}
	if arrayProp == nil {
		return items
	}

	out := NewJSONObject()
	for i := range resolved.Properties {
		prop := &resolved.Properties[i]
		switch {
		case prop.Name == arrayProp.Name:
			out.Set(prop.Name, items)
		case prop.Schema.Type.Value == "integer" && countLike.MatchString(prop.Name):
			out.Set(prop.Name, len(items))
		default:
			out.Set(prop.Name, synthesize(&prop.Schema, ctx, 0, prop.Name))
		}
	}
	return out
}
