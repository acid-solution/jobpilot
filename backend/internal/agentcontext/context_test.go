package agentcontext

import (
	"github.com/cloudwego/eino/schema"
	"testing"
)

func TestBudgetIncludesSchemasAndChinese(t *testing.T) {
	c := DefaultConfig()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.CompactAt = c.InputMax
	if c.Validate() == nil {
		t.Fatal("invalid soft limit accepted")
	}
	base := Count([]*schema.Message{schema.UserMessage("中文 JSON 测试")}, nil)
	if base < len([]byte("中文 JSON 测试")) {
		t.Fatal("Chinese underestimated")
	}
	if Count([]*schema.Message{schema.UserMessage("中文 JSON 测试")}, []*schema.ToolInfo{{Name: "long_tool_schema", Desc: "schema definitions count too"}}) <= base {
		t.Fatal("schema ignored")
	}
}
