package main

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

var (
	browserUARe = regexp.MustCompile(`(?i)curl|wget|fetch`)

	httpClient = &http.Client{Timeout: 10 * time.Second}
)

const htmlTemplate = `<!doctype html>
<meta charset="utf-8">
<title>%s — install</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 40rem; margin: 4rem auto; padding: 0 1rem; color: #1a1a1a; }
  code, pre { font-family: ui-monospace, monospace; background: #f4f4f4; padding: 0.1em 0.3em; border-radius: 3px; }
  pre { padding: 1rem; overflow-x: auto; }
  a { color: #2855c8; }
</style>
<h1>Install <code>%s</code></h1>
<p>This URL serves a shell install script. Run it like this:</p>
<pre>curl -fsSL https://%s/install | sh</pre>
<p>Source: <a href="https://github.com/%s/blob/main/scripts/install.sh">scripts/install.sh</a> · Releases: <a href="%s">github.com/%s/releases</a></p>
`

func Register(app *fiber.App, cfg *Config) {
	app.Get("/install", handleInstall(cfg))
	app.Use(notFound(cfg))
}

func handleInstall(cfg *Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		tool, ok := cfg.Lookup(c.Hostname())
		if !ok {
			return plain(c, fiber.StatusNotFound, "not found\n")
		}

		releasesPage := "https://github.com/" + tool.Repo + "/releases"

		if isBrowser(c) {
			c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
			c.Set(fiber.HeaderCacheControl, "public, max-age=300")
			return c.SendString(renderLanding(tool))
		}

		upstream := "https://github.com/" + tool.Repo + "/releases/latest/download/install.sh"

		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, upstream, nil)
		if err != nil {
			return plain(c, fiber.StatusBadGateway, fmt.Sprintf("upstream fetch failed: %s\n\nsee %s\n", err, releasesPage))
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return plain(c, fiber.StatusBadGateway, fmt.Sprintf("upstream fetch failed: %s\n\nsee %s\n", err, releasesPage))
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return plain(c, fiber.StatusServiceUnavailable, fmt.Sprintf(
				"no install script in the latest release of %s yet.\nthis usually means no release has been published, or the release is missing install.sh.\nsee %s\n",
				tool.Repo, releasesPage,
			))
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return plain(c, fiber.StatusBadGateway, fmt.Sprintf(
				"upstream returned %d %s\n\nsee %s\n",
				resp.StatusCode, http.StatusText(resp.StatusCode), releasesPage,
			))
		}

		c.Status(fiber.StatusOK)
		c.Set(fiber.HeaderContentType, "text/x-shellscript; charset=utf-8")
		c.Set(fiber.HeaderCacheControl, "public, max-age=300")
		c.Set("X-Source", upstream)
		return c.SendStream(resp.Body)
	}
}

func notFound(cfg *Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		if tool, ok := cfg.Lookup(c.Hostname()); ok {
			return plain(c, fiber.StatusNotFound, fmt.Sprintf(
				"not found\n\nsee https://github.com/%s/releases\n", tool.Repo,
			))
		}
		return plain(c, fiber.StatusNotFound, "not found\n")
	}
}

func isBrowser(c fiber.Ctx) bool {
	accept := c.Get(fiber.HeaderAccept)
	ua := c.Get(fiber.HeaderUserAgent)
	return strings.Contains(accept, "text/html") && !browserUARe.MatchString(ua)
}

func plain(c fiber.Ctx, status int, body string) error {
	c.Status(status)
	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, "public, max-age=60")
	return c.SendString(body)
}

func renderLanding(tool Tool) string {
	name := tool.DisplayName
	if name == "" {
		name = tool.Repo
	}
	escName := html.EscapeString(name)
	escHost := html.EscapeString(tool.Host)
	releasesPage := "https://github.com/" + tool.Repo + "/releases"
	return fmt.Sprintf(htmlTemplate, escName, escName, escHost, tool.Repo, releasesPage, tool.Repo)
}
