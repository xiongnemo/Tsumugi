package network

import "testing"

func TestResolveProxyEnvironmentEntry(t *testing.T) {
	cfg, err := ResolveProxy("", []string{"TSUMUGI_PROXY=socks5://user:pass@127.0.0.1:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind != ProxySOCKS5 || cfg.Source != SourceEnvironment || cfg.EnvVar != "TSUMUGI_PROXY" {
		t.Fatalf("unexpected proxy config: %+v", cfg)
	}

	entries := cfg.UIEntries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[1].Name != "Environment" || !entries[1].Active {
		t.Fatalf("missing active Environment entry: %+v", entries)
	}
	if cfg.MaskedAddress() != "***@127.0.0.1:1080" {
		t.Fatalf("masked address = %q", cfg.MaskedAddress())
	}
}

func TestParseMTProxyURL(t *testing.T) {
	cfg, err := ParseProxyURL("mtproxy://0123456789abcdef0123456789abcdef@proxy.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind != ProxyMTProxy {
		t.Fatalf("kind = %q", cfg.Kind)
	}
	if cfg.Secret != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("secret = %q", cfg.Secret)
	}
}
