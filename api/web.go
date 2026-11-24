package main

import (
	"embed"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

//go:embed web/index.html
var webFS embed.FS

// webRoutes serves the single-page UI at / and the Noto Sans Lao font from ../fonts.
func (a *API) webRoutes(r *gin.Engine) {
	r.GET("/", func(c *gin.Context) {
		b, err := webFS.ReadFile("web/index.html")
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", b)
	})
	r.GET("/fonts/NotoSansLao-Bold.ttf", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=86400")
		c.File(filepath.Join(a.cfg.ScriptDir, "fonts", "NotoSansLao-Bold.ttf"))
	})
}
