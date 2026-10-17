package market

import (
	"errors"
	"testing"
)

func TestValidateJDTextRejectsObviousGarbage(t *testing.T) {
	for _, text := range []string{"", "https://example.com/jobs/1234567890", "##############################", "123456789"} {
		if err := ValidateJDText(text); !errors.Is(err, ErrInvalidJDText) {
			t.Fatalf("expected invalid text, got %v", err)
		}
	}
	if err := ValidateJDText("负责 Go 后端服务开发，完成数据库建模、接口实现和自动化测试。"); err != nil {
		t.Fatal(err)
	}
}
