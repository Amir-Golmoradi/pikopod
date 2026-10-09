package curlline

import (
	"sort"
	"strings"
)

const (
	BaseVar       = "$PIKOPOD_URL"
	CredentialVar = "$PIKOPOD_CREDENTIAL"
)

var keptHeaders = map[string]bool{
	"content-type": true, "accept": true, "idempotency-key": true,
	"x-request-id": true, "x-correlation-id": true, "traceparent": true,
}

var credentialHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "x-api-key": true, "x-auth-token": true,
}

func Line(method, mount, path string, headers map[string]string, body []byte, credential string) string {
	target := BaseVar
	if mount != "" {
		target += "/" + strings.Trim(mount, "/")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	parts := []string{"curl -X " + strings.ToUpper(method) + " \"" + target + path + "\""}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		name := strings.ToLower(k)
		value := headers[k]
		switch {
		case credentialHeaders[name]:
			scheme := ""
			if i := strings.IndexByte(value, ' '); i > 0 && !strings.ContainsAny(value[:i], "[<") {
				scheme = value[:i+1]
			}
			parts = append(parts, "-H \""+name+": "+scheme+CredentialVar+"\"")
		case keptHeaders[name]:
			if credential != "" && strings.Contains(value, credential) {
				value = strings.ReplaceAll(value, credential, CredentialVar)
				parts = append(parts, "-H \""+name+": "+value+"\"")
				continue
			}
			parts = append(parts, "-H "+single(name+": "+value))
		}
	}
	if len(body) > 0 {
		text := string(body)
		if credential != "" {
			text = strings.ReplaceAll(text, credential, CredentialVar)
		}
		parts = append(parts, "--data "+single(text))
	}
	return strings.Join(parts, " ")
}

func single(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
