package modelusage

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"regexp"
)

type Price struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Currency string `json:"currency"`
	Input    string `json:"input_per_million"`
	CacheHit string `json:"cache_hit_per_million"`
	Output   string `json:"output_per_million"`
}
type Pricing struct {
	Version string  `json:"version"`
	Prices  []Price `json:"prices"`
}

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,12})?$`)

func LoadPricing(path string) (Pricing, error) {
	if path == "" {
		return Pricing{Version: "unconfigured", Prices: []Price{}}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Pricing{}, errors.New("cannot read MODEL_PRICING_FILE")
	}
	var p Pricing
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, errors.New("invalid model pricing JSON")
	}
	if p.Version == "" || len(p.Version) > 100 {
		return p, errors.New("pricing version is required")
	}
	seen := map[string]bool{}
	for _, rate := range p.Prices {
		key := rate.Provider + ":" + rate.Model
		if (rate.Provider != "deepseek" && rate.Provider != "bailian") || rate.Model == "" || (rate.Currency != "CNY" && rate.Currency != "USD") || seen[key] || !decimal.MatchString(rate.Input) || !decimal.MatchString(rate.CacheHit) || !decimal.MatchString(rate.Output) {
			return p, errors.New("invalid or duplicate model price")
		}
		seen[key] = true
	}
	return p, nil
}
func (p Pricing) Apply(c *Call) {
	if c.ReturnedModel == "" || c.UsageStatus != "known" {
		return
	}
	for _, rate := range p.Prices {
		if rate.Provider != c.Provider || rate.Model != c.ReturnedModel {
			continue
		}
		if !decimal.MatchString(rate.Input) || !decimal.MatchString(rate.CacheHit) || !decimal.MatchString(rate.Output) {
			return
		}
		inputRate, _ := new(big.Rat).SetString(rate.Input)
		hitRate, _ := new(big.Rat).SetString(rate.CacheHit)
		total := new(big.Rat)
		add := func(n int64, s string) {
			r, _ := new(big.Rat).SetString(s)
			total.Add(total, new(big.Rat).Mul(new(big.Rat).SetInt64(n), r))
		}
		if c.Provider == "bailian" {
			if c.Input == nil {
				return
			}
			add(*c.Input, rate.Input)
		} else {
			if c.Input == nil || c.Output == nil {
				return
			}
			if c.CacheHit != nil && c.CacheMiss != nil {
				add(*c.CacheHit, rate.CacheHit)
				add(*c.CacheMiss, rate.Input)
			} else if hitRate.Cmp(inputRate) == 0 {
				add(*c.Input, rate.Input)
			} else {
				return
			}
			add(*c.Output, rate.Output)
		}
		total.Quo(total, new(big.Rat).SetInt64(1000000))
		cost := total.FloatString(12)
		c.Cost = &cost
		c.Currency = rate.Currency
		c.PriceSnapshot, _ = json.Marshal(struct {
			Version string `json:"version"`
			Price   Price  `json:"price"`
		}{p.Version, rate})
		return
	}
}
