package locales

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestCatalogsHaveTheSameKeys guards the raw catalogs, metadata included.
// MyGo treats "@key" entries as placeholder metadata, but an extra one means
// one language documents an argument the other does not. The locale tag
// itself is the only key allowed to differ. Comparing metadata values as
// well as key names catches a placeholder declared as int in one language and
// String in the other.
func TestCatalogsHaveTheSameKeys(t *testing.T) {
	en := readLocale(t, "en.json")
	zh := readLocale(t, "zh-Hans.json")

	for key, zhValue := range zh {
		if key == "@@locale" {
			continue
		}
		enValue, ok := en[key]
		if !ok {
			t.Errorf("en.json is missing %q, which zh-Hans.json has", key)
			continue
		}
		if strings.HasPrefix(key, "@") && !reflect.DeepEqual(enValue, zhValue) {
			t.Errorf("%s metadata differs:\n  en: %#v\n  zh: %#v", key, enValue, zhValue)
		}
	}
	for key := range en {
		if key == "@@locale" {
			continue
		}
		if _, ok := zh[key]; !ok {
			t.Errorf("zh-Hans.json is missing %q, which en.json has", key)
		}
	}
}

func readLocale(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := files.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var catalog map[string]any
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return catalog
}
