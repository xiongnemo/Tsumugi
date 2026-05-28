package i18n

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestLocaleKeyParity(t *testing.T) {
	en := catalogEN
	zh := catalogZH
	if len(en) == 0 || len(zh) == 0 {
		t.Fatal("catalog not loaded")
	}
	var missingInZH, missingInEN []string
	for key := range en {
		if _, ok := zh[key]; !ok {
			missingInZH = append(missingInZH, key)
		}
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			missingInEN = append(missingInEN, key)
		}
	}
	sort.Strings(missingInZH)
	sort.Strings(missingInEN)
	if len(missingInZH) > 0 {
		t.Fatalf("zh missing keys: %v", missingInZH)
	}
	if len(missingInEN) > 0 {
		t.Fatalf("en missing keys: %v", missingInEN)
	}
}

func TestAllKeysNonEmpty(t *testing.T) {
	for key, value := range catalogEN {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("en key %q is empty", key)
		}
	}
	for key, value := range catalogZH {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("zh key %q is empty", key)
		}
	}
}

func TestTfPlaceholdersMatch(t *testing.T) {
	verb := regexp.MustCompile(`%[+#0-9.-]*[vTtbcdeEfFgGopqXxUds]`)
	for key, enVal := range catalogEN {
		zhVal := catalogZH[key]
		enSpecs := verb.FindAllString(enVal, -1)
		zhSpecs := verb.FindAllString(zhVal, -1)
		if fmt.Sprint(enSpecs) != fmt.Sprint(zhSpecs) {
			t.Fatalf("placeholder mismatch for %q: en=%v zh=%v", key, enSpecs, zhSpecs)
		}
	}
}

func TestMsgString(t *testing.T) {
	SetLocale("en")
	got := M(KeyStatusNewChannelMessage).String()
	if got != "New channel/group message." {
		t.Fatalf("got %q", got)
	}
	got = M(KeyStatusLoadingMediaPreviews, 3, 10).String()
	if got != "Loading media previews… (3/10)" {
		t.Fatalf("got %q", got)
	}
	SetLocale("zh")
	if got := M(KeyStatusNewChannelMessage).String(); got != "新频道/群组消息。" {
		t.Fatalf("got %q", got)
	}
	if got := M(KeyStatusLoadingMediaPreviews, 21, 29).String(); got != "正在加载媒体预览… (21/29)" {
		t.Fatalf("got %q", got)
	}
}

func TestCatalogJSONValid(t *testing.T) {
	for _, name := range []string{"locales/en.json", "locales/zh.json"} {
		raw, err := localeFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
