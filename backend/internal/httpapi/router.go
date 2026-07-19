package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"share/backend/internal/auth"
	"share/backend/internal/config"
	"share/backend/internal/email"
	"share/backend/internal/extraction"
	"share/backend/internal/ratelimit"
	"share/backend/internal/receipts"
	"share/backend/internal/store"
)

type Server struct {
	Pool      *pgxpool.Pool
	Cfg       *config.Config
	Auth      *auth.Manager
	Email     email.Provider
	RL        *ratelimit.Limiter
	Extractor extraction.Strategy
	Store     *store.Store
	Receipts  receipts.ReceiptStorage
}

func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(cors(s.Cfg))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api", func(api chi.Router) {
		// CSRF defense-in-depth: SameSite=Lax cookies alone aren't
		// sufficient (see docs), so reject any unsafe-method request that
		// isn't same-origin and carrying the header a cross-site HTML form
		// can't set.
		api.Use(requireSameOrigin(s.Cfg))
		// IP rate limit runs first — it only needs the raw request, not an
		// identified session — so abusive traffic gets rejected before
		// Identify can auto-provision an anonymous user+session row for it.
		api.Use(s.globalRateLimit)
		api.Use(s.Auth.Identify)
		api.Get("/me", s.handleMe)

		api.With(s.rateLimitOTPRequest).Post("/auth/otp/request", s.handleOTPRequest)
		api.With(s.rateLimitOTPVerify).Post("/auth/otp/verify", s.handleOTPVerify)
		api.Post("/auth/logout", s.handleLogout)

		if s.Cfg.PasskeyAccountsEnabled {
			api.Post("/auth/passkey/register/options", s.handlePasskeyRegisterOptions)
			api.Post("/auth/passkey/register/verify", s.handlePasskeyRegisterVerify)
			api.Post("/auth/passkey/login/options", s.handlePasskeyLoginOptions)
			api.Post("/auth/passkey/login/verify", s.handlePasskeyLoginVerify)
		}

		api.Post("/sessions", s.handleCreateSession)
		api.Get("/me/bills", s.handleListMyBills)
		api.Get("/sessions/{id}", s.handleGetSession)
		api.Patch("/sessions/{id}", s.handleUpdateSession)
		api.Get("/sessions/{id}/breakdown", s.handleBreakdown)
		api.Post("/sessions/{id}/share", s.handleCreateShare)

		api.Post("/sessions/{id}/people", s.handleAddPerson)
		api.Patch("/people/{personId}", s.handleRenamePerson)
		api.Delete("/people/{personId}", s.handleDeletePerson)

		api.Post("/sessions/{id}/dishes/bulk", s.handleReplaceDishes)
		api.Post("/sessions/{id}/dishes", s.handleAddDish)
		api.Patch("/dishes/{dishId}", s.handleUpdateDish)
		api.Delete("/dishes/{dishId}", s.handleDeleteDish)

		api.Put("/portions", s.handleUpsertPortion)

		api.Post("/sessions/{id}/receipt", s.handleUploadReceipt)
		api.Get("/sessions/{id}/receipt", s.handleGetReceipt)
		api.Post("/sessions/{id}/extract", s.handleExtract)
	})

	// Public, read-only share view — deliberately its own mount with no
	// Identify middleware, so a view-token request can never touch
	// session/auth machinery or reach a mutating handler.
	r.Route("/api/view", func(pub chi.Router) {
		pub.Get("/{token}", s.handlePublicView)
		pub.Get("/{token}/receipt", s.handlePublicReceipt)
	})

	return r
}

// requireSameOrigin rejects unsafe-method requests unless the Origin header
// matches the configured frontend origin and an X-Requested-With header is
// present. A cross-site HTML form can send neither reliably (no way to set
// custom headers, and the browser sets Origin to the attacker's own site),
// so this blocks classic CSRF even though SameSite=Lax cookies are also in
// play as a second layer.
func requireSameOrigin(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if cfg.CORSAllowedOrigin != "" {
				if origin := r.Header.Get("Origin"); origin != cfg.CORSAllowedOrigin {
					writeJSONError(w, http.StatusForbidden, "invalid origin")
					return
				}
			}
			if r.Header.Get("X-Requested-With") == "" {
				writeJSONError(w, http.StatusForbidden, "missing csrf header")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func cors(cfg *config.Config) func(http.Handler) http.Handler {
	origin := cfg.CORSAllowedOrigin
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With, X-Anon-Session-Token")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Expose-Headers", "X-Anon-Session-Token")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
