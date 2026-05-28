package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed locales/en.json locales/zh.json
var localeFS embed.FS

var (
	catalogMu sync.RWMutex
	catalogEN map[string]string
	catalogZH map[string]string
	reverseEN map[string]string
	reverseZH map[string]string
)

func init() {
	if err := loadCatalog(); err != nil {
		panic("i18n: load catalog: " + err.Error())
	}
}

func loadCatalog() error {
	en, err := readLocaleFile("locales/en.json")
	if err != nil {
		return err
	}
	zh, err := readLocaleFile("locales/zh.json")
	if err != nil {
		return err
	}
	catalogEN = en
	catalogZH = zh
	reverseEN = buildReverse(en)
	reverseZH = buildReverse(zh)
	return nil
}

func readLocaleFile(name string) (map[string]string, error) {
	raw, err := localeFS.ReadFile(name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func buildReverse(table map[string]string) map[string]string {
	out := make(map[string]string, len(table))
	for key, value := range table {
		out[value] = key
	}
	return out
}

func tableFor(localeCode string) map[string]string {
	switch localeCode {
	case "zh":
		return catalogZH
	default:
		return catalogEN
	}
}

// Tf renders a formatted translation for the current locale.
func Tf(key string, args ...any) string {
	template := T(key)
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

func knownKeyForText(text string) string {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if key, ok := reverseEN[text]; ok {
		return key
	}
	if key, ok := reverseZH[text]; ok {
		return key
	}
	return ""
}
