package config

import (
	"os"
	"strings"
	"testing"
)

func testValues() configValues {
	return configValues{
		"DATABASE_URL":              "postgres://test@localhost/test",
		"CREDENTIAL_ENCRYPTION_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
	}
}

func TestWorkerCountDefaultAndValidation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 2}, {"1", 1}, {"2", 2}, {"7", 7},
		{"0", 0}, {"-1", 0}, {"2.5", 0}, {"many", 0}, {"99999999999999999999999999", 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			values := testValues()
			values["WORKER_COUNT"] = tc.value
			cfg, err := parse(values)
			if tc.want == 0 {
				if err == nil || err.Error() != "WORKER_COUNT must be a positive integer" {
					t.Fatalf("invalid count accepted: config=%#v err=%v", cfg, err)
				}
			} else if err != nil || cfg.WorkerCount != tc.want {
				t.Fatalf("count=%d want=%d err=%v", cfg.WorkerCount, tc.want, err)
			}
		})
	}
}

func TestLoadDotEnvAndEnvironmentPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range testValues() {
		t.Setenv(key, value)
	}
	// t.Setenv tracks restoration, even if the variable initially existed.
	t.Setenv("WORKER_COUNT", "")
	if err := os.Unsetenv("WORKER_COUNT"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if err := os.WriteFile(".env", []byte("WORKER_COUNT=4\nGITHUB_TOKEN=file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.WorkerCount != 4 || cfg.GitHubToken != "" {
		t.Fatalf("file config / explicit empty override failed: count=%d token_empty=%v err=%v", cfg.WorkerCount, cfg.GitHubToken == "", err)
	}
	if _, set := os.LookupEnv("WORKER_COUNT"); set {
		t.Fatal("loading .env modified process environment")
	}
	t.Setenv("WORKER_COUNT", "3")
	cfg, err = Load()
	if err != nil || cfg.WorkerCount != 3 {
		t.Fatalf("environment override: count=%d err=%v", cfg.WorkerCount, err)
	}
}

func TestMissingDotEnvAndSafeParseError(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range testValues() {
		t.Setenv(key, value)
	}
	t.Setenv("WORKER_COUNT", "")
	cfg, err := Load()
	if err != nil || cfg.WorkerCount != 2 {
		t.Fatalf("missing optional .env: %v", err)
	}
	if err := os.WriteFile(".env", []byte("BAD_KEY*=private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Load()
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("unsafe config error: %v", err)
	}
}
