package compose

import (
	"regexp"
	"strconv"
	"strings"

	"truffels-api/internal/model"
	"truffels-api/internal/service"
)

// WebRoutesFrom collects the web routes of every registered service that
// declares one — the set the proxy exposes and the UI links to.
func WebRoutesFrom(reg *service.Registry) []model.WebRoute {
	var routes []model.WebRoute
	for _, tmpl := range reg.All() {
		if tmpl.Web != nil {
			routes = append(routes, *tmpl.Web)
		}
	}
	return routes
}

// caddyRouteRe/caddyHostRe bound what a catalog web route can inject into the
// Caddyfile: a rooted path and a hostname. Anything else is skipped rather than
// written, so a bad entry can never produce a Caddyfile that fails to parse.
var (
	caddyRouteRe = regexp.MustCompile(`^/[A-Za-z0-9/_-]+$`)
	caddyHostRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// RenderCaddyfile returns the Caddyfile for the proxy, with a handle block for
// each installed catalog web service spliced in before the mempool catch-all.
// The first handle block (/proxy-health) is a static OK response so the proxy
// healthcheck doesn't depend on any upstream.
func RenderCaddyfile(webRoutes []model.WebRoute) string {
	var b strings.Builder
	for _, wr := range webRoutes {
		if !caddyRouteRe.MatchString(wr.Route) || !caddyHostRe.MatchString(wr.Container) || wr.Port <= 0 || wr.Port > 65535 {
			continue
		}
		b.WriteString("\thandle " + wr.Route + "* {\n")
		b.WriteString("\t\theader Content-Security-Policy \"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'\"\n")
		b.WriteString("\t\treverse_proxy " + wr.Container + ":" + strconv.Itoa(wr.Port) + "\n")
		b.WriteString("\t}\n")
	}
	return strings.Replace(caddyfileTemplate, "%CATALOG_WEB_ROUTES%\n", b.String(), 1)
}

const caddyfileTemplate = `{
	auto_https off
	admin off
}

:80 {
	# Access log. The proxy is the only component that ever sees a real client
	# address: everything behind it — the web container's nginx, the API — logs
	# 172.21.0.x, which is this proxy. Without this block that address exists
	# nowhere, and "did that device reach us at all" is unanswerable. It was
	# unanswerable in dev.29, when a phone could not load the UI and the only way
	# to look was firewall counters and inference.
	#
	# stdout so it lands in docker logs alongside every other service; JSON
	# because that is already the format Caddy's own error lines use.
	log {
		output stdout
		format json
	}

	# Shared security headers (non-CSP)
	header {
		X-Content-Type-Options nosniff
		X-Frame-Options SAMEORIGIN
		Referrer-Policy no-referrer
		Permissions-Policy "camera=(), microphone=(), geolocation=()"
		-Server
	}

	# Self-contained health endpoint — used by the proxy's container
	# healthcheck. Returns 200 regardless of upstream state.
	#
	# log_skip because the healthcheck runs every 30s: without it this one path
	# writes ~2900 lines a day and buries the traffic the log exists to show.
	handle /proxy-health {
		log_skip
		respond "OK" 200
	}

	# ckstats — Next.js requires unsafe-inline for SSR hydration scripts
	handle /ckstats* {
		header Content-Security-Policy "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-ckstats:3000
	}

	# Truffels control plane
	handle /admin* {
		header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-web:8080
	}

	handle /api/truffels/* {
		header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-api:8080
	}

	# Mempool — all traffic (API + websocket + frontend) goes through
	# the mempool frontend nginx, which handles /api/ → /api/v1/ rewrite.
	handle /ws {
		header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-mempool-frontend:8080
	}
%CATALOG_WEB_ROUTES%
	handle {
		header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-mempool-frontend:8080
	}
}
`
