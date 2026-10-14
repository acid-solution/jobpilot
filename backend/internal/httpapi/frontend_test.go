package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFrontendRoutesAndCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "assets"), 0700)
	for name, body := range map[string]string{"index.html": "<html>frontend</html>", "assets/index-a1b2c3d4.js": "export {}", "favicon.svg": "<svg/>"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewFrontend(root)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/health/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	router.NoRoute(handler)
	for _, tc := range []struct {
		method, url, accept string
		status              int
		cache               string
	}{
		{"GET", "/", "", 200, "no-cache"},
		{"HEAD", "/", "", 200, "no-cache"},
		{"GET", "/profile", "text/html", 200, "no-cache"},
		{"GET", "/profile/", "text/html", 200, "no-cache"},
		{"GET", "/profile", "application/json", 404, ""},
		{"GET", "/profile", "text/html;q=0.000", 404, ""},
		{"POST", "/profile", "text/html", 404, ""},
		{"GET", "/assets/index-a1b2c3d4.js", "*/*", 200, "public, max-age=31536000, immutable"},
		{"GET", "/favicon.svg", "*/*", 200, "no-cache"},
		{"GET", "/assets/missing.js", "text/html", 404, ""},
		{"GET", "/assets", "text/html", 404, ""},
		{"GET", "/missing.css", "text/html", 404, ""},
		{"GET", "/api/unknown", "text/html", 404, ""},
		{"GET", "/api", "text/html", 404, ""},
		{"GET", "/auth/unknown", "text/html", 404, ""},
		{"GET", "/health/unknown", "text/html", 404, ""},
		{"GET", "/dev/outbox", "text/html", 404, ""},
		{"GET", "/../outside", "text/html", 404, ""},
		{"GET", "/%2e%2e/outside", "text/html", 404, ""},
		{"GET", "/.env", "text/html", 404, ""},
		{"GET", "/assets/%5c../outside", "text/html", 404, ""},
		{"GET", "/health/live", "application/json", 200, ""},
	} {
		t.Run(tc.method+tc.url+tc.accept, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.url, nil)
			request.Header.Set("Accept", tc.accept)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tc.status || response.Header().Get("Cache-Control") != tc.cache {
				t.Fatalf("status=%d cache=%s", response.Code, response.Header().Get("Cache-Control"))
			}
			if tc.method == "HEAD" && response.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
			if tc.status == 404 && strings.Contains(response.Body.String(), "<html>") {
				t.Fatal("unknown resource became SPA HTML")
			}
		})
	}
	initial := httptest.NewRecorder()
	router.ServeHTTP(initial, httptest.NewRequest("GET", "/", nil))
	conditional := httptest.NewRequest("GET", "/", nil)
	conditional.Header.Set("If-Modified-Since", initial.Header().Get("Last-Modified"))
	checked := httptest.NewRecorder()
	router.ServeHTTP(checked, conditional)
	if checked.Code != http.StatusNotModified || checked.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("index revalidation failed")
	}
}

func TestFrontendConfigurationAndSymlinkBoundary(t *testing.T) {
	if h, e := NewFrontend(""); e != nil || h != nil {
		t.Fatal("development mode changed")
	}
	root := t.TempDir()
	if _, e := NewFrontend(filepath.Join(root, "missing")); e == nil {
		t.Fatal("missing root accepted")
	}
	if _, e := NewFrontend(root); e == nil {
		t.Fatal("missing index accepted")
	}
	os.WriteFile(filepath.Join(root, "index.html"), []byte("index"), 0600)
	if _, e := NewFrontend(filepath.Join(root, "index.html")); e == nil {
		t.Fatal("file used as root")
	}
	secret := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(secret, []byte("private"), 0600)
	if err := os.Symlink(secret, filepath.Join(root, "linked.txt")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	handler, err := NewFrontend(root)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.NoRoute(handler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/linked.txt", nil)
	req.Header.Set("Accept", "text/html")
	r.ServeHTTP(w, req)
	if w.Code != 404 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("symlink escaped frontend root")
	}
}
