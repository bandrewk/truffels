package compose

// RenderCaddyfile returns the canonical Caddyfile content for the proxy
// service. The first handle block (/proxy-health) is a static OK response
// so the proxy healthcheck doesn't depend on any upstream — fixes the
// dev.14 false-positive where stopping mempool marked Caddy unhealthy.
func RenderCaddyfile() string {
	return caddyfileTemplate
}

const caddyfileTemplate = `{
	auto_https off
	admin off
}

:80 {
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
	handle /proxy-health {
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

	handle {
		header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws:; img-src 'self' data:; font-src 'self'"
		reverse_proxy truffels-mempool-frontend:8080
	}
}
`
