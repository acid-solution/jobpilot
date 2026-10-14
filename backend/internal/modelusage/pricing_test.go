package modelusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPriceConfigurationAndSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "prices.json")
	for _, raw := range []string{`{"version":"v1","prices":[{"provider":"deepseek","model":"test","currency":"CNY","input_per_million":"-1","cache_hit_per_million":"0","output_per_million":"1"}]}`, `{"version":"","prices":[]}`} {
		os.WriteFile(file, []byte(raw), 0600)
		if _, err := LoadPricing(file); err == nil {
			t.Fatal("invalid pricing accepted")
		}
	}
	book := Pricing{Version: "v1", Prices: []Price{{Provider: "bailian", Model: "text-embedding-v4", Currency: "USD", Input: "0.5", CacheHit: "0", Output: "0"}}}
	raw, _ := json.Marshal(book)
	os.WriteFile(file, raw, 0600)
	loaded, err := LoadPricing(file)
	if err != nil {
		t.Fatal(err)
	}
	n := int64(27)
	c := Call{Provider: "bailian", ReturnedModel: "text-embedding-v4", UsageStatus: "known", Tokens: Tokens{Input: &n}}
	loaded.Apply(&c)
	if c.Cost == nil || *c.Cost != "0.000013500000" || c.Currency != "USD" {
		t.Fatal("embedding fee incorrect")
	}
	snapshot := string(c.PriceSnapshot)
	loaded.Prices[0].Input = "9"
	if string(c.PriceSnapshot) != snapshot {
		t.Fatal("historical price changed")
	}
	unknown := Call{Provider: "bailian", ReturnedModel: "other-model", UsageStatus: "known", Tokens: Tokens{Input: &n}}
	loaded.Apply(&unknown)
	if unknown.Cost != nil {
		t.Fatal("unmatched model billed")
	}
}
