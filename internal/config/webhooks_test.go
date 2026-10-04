package config

import (
	"strings"
	"testing"
)

func TestWebhookReceiverIsParsedAndValidated(t *testing.T) {
	cfg, err := Load(writeCfg(t, "upstreams:\n  examplepay:\n    target: https://api.examplepay.co\n    webhooks:\n      receiver: http://localhost:3000/hooks/examplepay\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstreams["examplepay"].Webhooks == nil || cfg.Upstreams["examplepay"].Webhooks.Receiver != "http://localhost:3000/hooks/examplepay" {
		t.Fatalf("receiver not read: %+v", cfg.Upstreams["examplepay"])
	}
	plain, err := Load(writeCfg(t, "upstreams:\n  examplepay:\n    target: https://api.examplepay.co\n"))
	if err != nil || plain.Upstreams["examplepay"].Webhooks != nil {
		t.Fatalf("absent means absent: %+v %v", plain.Upstreams["examplepay"], err)
	}
	if _, err := Load(writeCfg(t, "upstreams:\n  examplepay:\n    target: https://api.examplepay.co\n    webhooks:\n      receiver: localhost:3000\n")); err == nil || !strings.Contains(err.Error(), "webhooks.receiver") {
		t.Fatalf("a relative receiver is refused by name: %v", err)
	}
}
