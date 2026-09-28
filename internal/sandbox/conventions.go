package sandbox

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/pikopod/pikopod/internal/ir"
)

var (
	camelBoundary   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	statusLikeName  = regexp.MustCompile(`^(status|success|successful|succeeded|ok|valid)$`)
	messageName     = regexp.MustCompile(`^message$`)
	timeLikeName    = regexp.MustCompile(`(^|_)(at|date|time|timestamp)$|^(created|updated|modified|timestamp)(_at)?$`)
	tokenLikeName   = regexp.MustCompile(`^(id|reference|ref|code|token|key)$|_(code|ref|id|token|key)$`)
	urlLikeName     = regexp.MustCompile(`(^|_)(url|uri|link|href)$`)
	domainName      = regexp.MustCompile(`^domain$`)
	currencyName    = regexp.MustCompile(`(^|_)currency$`)
	amountLikeName  = regexp.MustCompile(`^(amount|total|price|fee|balance)$|_(amount|total|fee|price)$`)
	countLikeName   = regexp.MustCompile(`^(quantity|count)$|_count$`)
	createdLikeName = regexp.MustCompile(`^created(_at|_on|_date)?$|^creation(_date|_time)?$`)
)

func snakeName(name string) string {
	return strings.ToLower(camelBoundary.ReplaceAllString(name, "${1}_${2}"))
}

func constraintValue(schema *ir.IrSchemaNode, key string) (any, bool) {
	for _, c := range schema.Constraints {
		if c.Key == key {
			return c.Value.Value, true
		}
	}
	return nil, false
}

func declaredValue(schema *ir.IrSchemaNode) (any, string, bool) {
	for _, key := range []string{"example", "default"} {
		v, ok := constraintValue(schema, key)
		if !ok {
			continue
		}
		if coerced, ok := coerceToType(schema.Type.Value, v); ok {
			return coerced, key, true
		}
	}
	return nil, "", false
}

func coerceToType(typ string, v any) (any, bool) {
	switch typ {
	case "string":
		s, ok := v.(string)
		return s, ok
	case "boolean":
		b, ok := v.(bool)
		return b, ok
	case "integer":
		f, ok := asFloat(v)
		if !ok || f != float64(int64(f)) {
			return nil, false
		}
		return int(f), true
	case "number":
		f, ok := asFloat(v)
		return f, ok
	}
	return nil, false
}

func (ctx *synthContext) succeeded() bool {
	return ctx.status == 0 || ctx.status < 400
}

func (ctx *synthContext) note(field, source string) {
	if ctx.trace != nil && field != "" {
		ctx.trace(field, source)
	}
}

func tokenPrefix(snake string) string {
	if i := strings.LastIndex(snake, "_"); i > 0 {
		stem := snake[:i]
		if j := strings.LastIndex(stem, "_"); j >= 0 {
			stem = stem[j+1:]
		}
		if len(stem) > 3 {
			stem = stem[:3]
		}
		return stem + "_"
	}
	switch snake {
	case "reference", "ref":
		return "ref_"
	case "token":
		return "tok_"
	}
	return snake + "_"
}

func conventionBool(ctx *synthContext, fieldName string) (bool, bool) {
	if !statusLikeName.MatchString(snakeName(fieldName)) {
		return false, false
	}
	return ctx.succeeded(), true
}

func conventionString(ctx *synthContext, fieldName string) (string, string, bool) {
	snake := snakeName(fieldName)
	p := ctx.prng
	switch {
	case messageName.MatchString(snake):
		if ctx.description != "" {
			return ctx.description, "convention description", true
		}
		if ctx.succeeded() {
			return "ok", "convention ok", true
		}
		return http.StatusText(ctx.status), "convention reason phrase", true
	case timeLikeName.MatchString(snake):
		return isoFrom(ctx.virtualClockMs, false), "convention timestamp", true
	case tokenLikeName.MatchString(snake):
		prefix := tokenPrefix(snake)
		return prefix + p.Token(14), "convention " + prefix, true
	case urlLikeName.MatchString(snake):
		return "https://" + ctx.sandbox + ".test/" + p.Word(), "convention url", true
	case domainName.MatchString(snake):
		return "test", "convention domain", true
	case currencyName.MatchString(snake):
		return "USD", "convention currency", true
	}
	return "", "", false
}

func conventionNumber(ctx *synthContext, fieldName string, integer bool) (any, string, bool) {
	snake := snakeName(fieldName)
	switch {
	case amountLikeName.MatchString(snake):
		n := ctx.prng.Int(100, 100000)
		if integer {
			return n, "convention amount", true
		}
		return float64(n), "convention amount", true
	case countLikeName.MatchString(snake):
		n := ctx.prng.Int(1, 100)
		if integer {
			return n, "convention count", true
		}
		return float64(n), "convention count", true
	}
	return nil, "", false
}

func hasSource(schema *ir.IrSchemaNode, fieldName string) bool {
	if _, _, ok := declaredValue(schema); ok {
		return true
	}
	if schema.EnumValues != nil && len(schema.EnumValues.Value) > 0 {
		return true
	}
	if schema.Format != nil && schema.Format.Value != "" {
		return true
	}
	snake := snakeName(fieldName)
	lname := strings.ToLower(fieldName)
	switch schema.Type.Value {
	case "string":
		return messageName.MatchString(snake) || timeLikeName.MatchString(snake) || tokenLikeName.MatchString(snake) ||
			urlLikeName.MatchString(snake) || domainName.MatchString(snake) || currencyName.MatchString(snake) || lnameEmail.MatchString(lname)
	case "boolean":
		return statusLikeName.MatchString(snake)
	}
	return true
}

func RealismLint(def *ir.ApiDefinition) (int, []string) {
	named := map[string]*ir.IrSchemaNode{}
	for i := range def.Schemas {
		named[def.Schemas[i].ID] = &def.Schemas[i].Schema
	}
	seen := map[string]bool{}
	var names []string
	count := 0
	var walk func(schema *ir.IrSchemaNode, fieldName, key string, depth int)
	walk = func(schema *ir.IrSchemaNode, fieldName, key string, depth int) {
		if schema == nil || depth > synthMaxDepth {
			return
		}
		if schema.Ref != nil {
			target, ok := named[*schema.Ref]
			if !ok {
				return
			}
			walk(target, fieldName, *schema.Ref, depth+1)
			return
		}
		if schema.Composition != nil && len(schema.Composition.Members) > 0 {
			if schema.Composition.Kind == "allOf" {
				for i := range schema.Composition.Members {
					walk(&schema.Composition.Members[i], fieldName, key+":"+strconv.Itoa(i), depth+1)
				}
				return
			}
			walk(&schema.Composition.Members[0], fieldName, key+":0", depth+1)
			return
		}
		switch schema.Type.Value {
		case "object":
			for i := range schema.Properties {
				p := &schema.Properties[i]
				walk(&p.Schema, p.Name, key+"."+p.Name, depth+1)
			}
		case "array":
			walk(schema.Items, fieldName, key+"[]", depth+1)
		case "string", "boolean":
			if seen[key] || hasSource(schema, fieldName) {
				return
			}
			seen[key] = true
			count++
			if len(names) < 3 && !containsString(names, fieldName) {
				names = append(names, fieldName)
			}
		}
	}
	for i := range def.Endpoints {
		ep := &def.Endpoints[i]
		for j := range ep.Responses {
			r := &ep.Responses[j]
			if len(r.StatusCode) == 0 || r.StatusCode[0] != '2' {
				continue
			}
			if schema := jsonContent(r); schema != nil {
				walk(schema, "", ep.ID+":"+r.StatusCode, 0)
			}
		}
	}
	return count, names
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func RealismLine(def *ir.ApiDefinition) string {
	count, names := RealismLint(def)
	line := strconv.Itoa(count) + " field(s) synthesised without spec, example or convention"
	if len(names) > 0 {
		line += ": " + strings.Join(names, ", ")
		if count > len(names) {
			line += ", …"
		}
	}
	return line
}

func preserveCreatedFields(attrs *JSONObject, current json.RawMessage) {
	previous, ok := parseJSONValueOK(current)
	if !ok {
		return
	}
	for _, k := range previous.Keys() {
		if createdLikeName.MatchString(snakeName(k)) {
			if v, has := previous.Get(k); has {
				attrs.Set(k, v)
			}
		}
	}
}
