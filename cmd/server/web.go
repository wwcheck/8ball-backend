package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// webRoot is where the Unity WebGL build is unpacked on the origin server.
// EdgeOne (the CDN) back-origins to :8080, so the Go process must serve
// index.html, /Build/* and /TemplateData/* itself.
const webRoot = "/data/8ball-backend/web"

// registerWebGL mounts the Unity WebGL static-file routes. Kept in its own
// file so the WebGL serving rules (gzip passthrough + wasm/js MIME) live in
// one clearly scoped place, leaving main.go's existing routes untouched.
func registerWebGL(r *gin.Engine) {
	h := serveWebGLFile
	r.GET("/", h)
	r.GET("/Build/*filepath", h)
	r.GET("/TemplateData/*filepath", h)
}

// serveWebGLFile writes the file under webRoot that matches the request path.
// It uses the full decoded request path (not gin's :filepath param) so the
// leading segment — Build/ or TemplateData/ — is preserved in the lookup.
func serveWebGLFile(c *gin.Context) {
	rel := strings.TrimPrefix(c.Request.URL.Path, "/")
	if rel == "" {
		rel = "index.html"
	}

	// Path-traversal guard: normalize, then require the result to stay under
	// webRoot (no "..", no absolute paths, no bare ".").
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(clean) {
		c.Status(http.StatusNotFound)
		return
	}
	full := filepath.Join(webRoot, clean)

	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}

	// Unity WebGL ships pre-compressed assets as ".gz" files. For those we
	// return the raw bytes with Content-Encoding: gzip and let the browser's
	// loader.js decompress — we must NOT re-gzip (double compression) nor
	// decompress-and-serve (the client would receive inflated bytes without
	// the gzip header and fail).
	var extra map[string]string
	if strings.HasSuffix(clean, ".gz") {
		extra = map[string]string{"Content-Encoding": "gzip"}
	}

	f, err := os.Open(full)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()

	// mimeTypeFor resolves the type against the un-gzipped name, so
	// "x.wasm.gz" → application/wasm (not application/gzip).
	c.DataFromReader(http.StatusOK, info.Size(), mimeTypeFor(clean), f, extra)
}

// mimeTypeFor returns the Content-Type for a path. The critical WebGL types
// are pinned explicitly (the Go mime table maps ".js" to text/javascript,
// which breaks Unity's loader); everything else falls back to the stdlib.
func mimeTypeFor(name string) string {
	base := name
	if strings.HasSuffix(base, ".gz") {
		base = strings.TrimSuffix(base, ".gz")
	}
	switch {
	case strings.HasSuffix(base, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(base, ".js"):
		return "application/javascript"
	case strings.HasSuffix(base, ".data"):
		return "application/octet-stream"
	case strings.HasSuffix(base, ".css"):
		return "text/css"
	case strings.HasSuffix(base, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(base, ".ico"):
		return "image/x-icon"
	}
	if t := mime.TypeByExtension(filepath.Ext(base)); t != "" {
		return t
	}
	return "application/octet-stream"
}
