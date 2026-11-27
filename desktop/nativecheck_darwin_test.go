//go:build nativecheck

package main

import (
	"reflect"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// TestProbeTextsCoverLocales keeps the native probe able to read the page in
// every language the page can be set to.
func TestProbeTextsCoverLocales(t *testing.T) {
	texts := probeTexts()
	for _, locale := range usage.Locales() {
		text, ok := texts[locale]
		if !ok {
			t.Errorf("no probe texts for locale %q", locale)
			continue
		}
		fields := reflect.ValueOf(text)
		for i := range fields.NumField() {
			if fields.Field(i).String() == "" {
				t.Errorf("probe text %s is empty for locale %q", fields.Type().Field(i).Name, locale)
			}
		}
	}
	if _, ok := texts[usage.LocaleDefault]; !ok {
		t.Errorf("no probe texts for the default locale %q", usage.LocaleDefault)
	}
}
