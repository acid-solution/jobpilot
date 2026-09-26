package abilityidentity

import "testing"

func TestNormalizeName(t *testing.T) {
	for _, value := range []string{"AutoGen", "ＡｕｔｏＧｅｎ", "Auto·Gen", " Auto_Gen ", "Auto-Gen", "Auto/Gen", "Auto:Gen"} {
		if got := NormalizeName(value); got != "autogen" {
			t.Fatalf("%q normalized to %q", value, got)
		}
	}
	if NormalizeName("C++") == NormalizeName("C") || NormalizeName("C#") == NormalizeName("C") || NormalizeName("C++") == NormalizeName("C#") {
		t.Fatal("different programming languages were merged")
	}
}
