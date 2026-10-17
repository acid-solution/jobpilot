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

func TestAtomicLabelRejectsListsWithoutRejectingTechnicalNames(t *testing.T) {
	for _, name := range []string{"CI/CD", "数据结构与算法", "高等数学", "不等式", "等级保护"} {
		if !IsAtomicLabel(name) {
			t.Errorf("legitimate technical name rejected: %s", name)
		}
	}
	for _, name := range []string{"MySQL、PostgreSQL", "Go等语言", "Wireshark等抓包工具", "SSH等常用协议", "有相关项目实践经历者优先"} {
		if IsAtomicLabel(name) {
			t.Errorf("composite or sentence label accepted: %s", name)
		}
	}
}
