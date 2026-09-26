package embedding

import (
	"context"
	"math"
	"os"
	"testing"
	"time"
)

// Opt in explicitly: a live request uses the configured platform API key.
func TestLiveBailianEmbedding(t *testing.T) {
	if os.Getenv("RUN_BAILIAN_LIVE_TEST") != "1" {
		t.Skip("set RUN_BAILIAN_LIVE_TEST=1 to call Bailian")
	}
	client := NewClient(os.Getenv("EMBEDDING_BASE_URL"), os.Getenv("PLATFORM_EMBEDDING_API_KEY"), nil)
	if !client.Configured() {
		t.Fatal("Bailian endpoint or platform key is missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	vectors, err := client.Embed(ctx, []string{"Go 后端开发：设计和实现可靠的岗位分析服务。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != Dimensions {
		t.Fatalf("unexpected embedding shape: vectors=%d", len(vectors))
	}
	var norm float64
	for _, value := range vectors[0] {
		norm += float64(value) * float64(value)
	}
	if math.IsNaN(norm) || math.IsInf(norm, 0) || norm == 0 {
		t.Fatalf("invalid embedding norm: %g", norm)
	}
	t.Logf("Bailian %s returned %d dimensions, norm %.4f", Model, len(vectors[0]), math.Sqrt(norm))
}
