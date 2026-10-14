package httpapi

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var hashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9.]+$`)

// NewFrontend validates a deployment's built files before server startup.
// An empty directory keeps the development API-only mode.
func NewFrontend(directory string) (gin.HandlerFunc, error) {
	if directory == "" {
		return nil, nil
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, errors.New("invalid FRONTEND_DIST_DIR")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("FRONTEND_DIST_DIR is not accessible")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("FRONTEND_DIST_DIR must be a directory")
	}
	open := func(name string) (*os.File, error) {
		// OpenInRoot also prevents a symlink swap between path validation and open.
		file, err := os.OpenInRoot(root, name)
		if err != nil {
			return nil, err
		}
		stat, err := file.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			file.Close()
			return nil, fs.ErrPermission
		}
		return file, nil
	}
	index, err := open("index.html")
	if err != nil {
		return nil, errors.New("FRONTEND_DIST_DIR requires a readable index.html")
	}
	index.Close()
	serve := func(c *gin.Context, file *os.File, name string) {
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-cache")
		if name != "index.html" && strings.HasPrefix(name, "assets/") && hashedAsset.MatchString(path.Base(name)) {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeContent(c.Writer, c.Request, path.Base(name), info.ModTime(), file)
	}
	return func(c *gin.Context) {
		notFound := func() { writeError(c, http.StatusNotFound, "not_found", "资源不存在") }
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			notFound()
			return
		}
		requestPath := c.Request.URL.Path
		for _, prefix := range []string{"/api", "/auth", "/health", "/dev"} {
			if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
				notFound()
				return
			}
		}
		name := strings.Trim(strings.TrimPrefix(requestPath, "/"), "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || strings.Contains(name, `\`) {
			notFound()
			return
		}
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(part, ".") {
				notFound()
				return
			}
		}
		file, err := open(name)
		if err == nil {
			serve(c, file, name)
			return
		}
		// Directories, forbidden symlinks, missing assets and non-navigation requests
		// must not become a successful HTML response.
		if !errors.Is(err, fs.ErrNotExist) || name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" || !acceptsHTML(c.Request.Header.Get("Accept")) {
			notFound()
			return
		}
		file, err = open("index.html")
		if err != nil {
			notFound()
			return
		}
		serve(c, file, "index.html")
	}, nil
}

func acceptsHTML(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || (media != "text/html" && media != "application/xhtml+xml") {
			continue
		}
		if q, ok := params["q"]; ok {
			weight, err := strconv.ParseFloat(q, 64)
			if err != nil || weight <= 0 || weight > 1 {
				continue
			}
		}
		return true
	}
	return false
}
