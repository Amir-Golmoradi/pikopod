package scenario

import (
	"fmt"
	"sort"
	"strings"
)

var knownCaptureSources = []string{"response.body", "response.headers", "webhook.delivery", "state.resource"}

type parsedCapture struct {
	source string
	path   string
}

func parseCaptureExpr(expr string) (parsedCapture, error) {
	dollar := strings.Index(expr, "$")
	if dollar == -1 {
		return parsedCapture{}, fmt.Errorf("capture '%s' must contain a '$' separating source from JSONPath", expr)
	}
	source := expr[:dollar]
	path := expr[dollar:]
	for _, s := range knownCaptureSources {
		if s == source {
			return parsedCapture{source: source, path: path}, nil
		}
	}
	return parsedCapture{}, fmt.Errorf("capture source '%s' is not one of %s", source, strings.Join(knownCaptureSources, ", "))
}

type captureDocuments map[string]any

var envelopeKeys = []string{"data", "result", "payload", "resource", "object", "item"}

func resolveThroughEnvelope(doc any, path string) (bool, any, string) {
	found, value, err := getByPath(doc, path)
	if err != nil {
		return false, nil, path
	}
	if found {
		return true, value, path
	}
	obj, ok := doc.(map[string]any)
	if !ok || !strings.HasPrefix(path, "$.") {
		return false, nil, path
	}
	candidates := []string{}
	for _, k := range envelopeKeys {
		if _, has := obj[k]; has {
			candidates = append(candidates, k)
		}
	}
	if len(candidates) == 0 {
		objectKeys := []string{}
		for k, v := range obj {
			if _, isObj := v.(map[string]any); isObj {
				objectKeys = append(objectKeys, k)
			}
		}
		if len(objectKeys) == 1 {
			candidates = objectKeys
		}
	}
	for _, k := range candidates {
		inner, isObj := obj[k].(map[string]any)
		if !isObj {
			continue
		}
		if innerFound, innerValue, err := getByPath(inner, path); err == nil && innerFound {
			return true, innerValue, "$." + k + path[1:]
		}
	}
	return false, nil, path
}

func applyCaptures(captureMap map[string]string, docs captureDocuments) (map[string]any, map[string]string, error) {
	out := map[string]any{}
	resolved := map[string]string{}
	names := make([]string, 0, len(captureMap))
	for name := range captureMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		expr := captureMap[name]
		pc, err := parseCaptureExpr(expr)
		if err != nil {
			return nil, nil, err
		}
		doc, has := docs[pc.source]
		if !has {
			return nil, nil, fmt.Errorf("capture '%s' reads '%s', which this step did not produce", name, pc.source)
		}
		if _, _, err := getByPath(doc, pc.path); err != nil {
			return nil, nil, err
		}
		found, value, path := resolveThroughEnvelope(doc, pc.path)
		if !found {
			return nil, nil, fmt.Errorf("capture '%s' path '%s' did not resolve against %s", name, pc.path, pc.source)
		}
		out[name] = value
		resolved[name] = path
	}
	return out, resolved, nil
}
