package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"cher-app/backend/internal/auth"
	"cher-app/backend/internal/config"
	"cher-app/backend/internal/email"
	"cher-app/backend/internal/llm"
	"cher-app/backend/internal/ratelimit"
	"cher-app/backend/internal/receipts"
	"cher-app/backend/internal/store"
)

type Server struct {
	Pool     *pgxpool.Pool
	Cfg      *config.Config
	Auth     *auth.Manager
	Email    email.Provider
	RL       *ratelimit.Limiter
	LLM      llm.Provider
	Store    *store.Store
	Receipts *receipts.Storage
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
		api.Use(s.Auth.Identify)
		api.Use(s.globalRateLimit)
		api.Get("/me", s.handleMe)

		api.With(s.rateLimitOTPRequest).Post("/auth/otp/request", s.handleOTPRequest)
		api.Post("/auth/otp/verify", s.handleOTPVerify)
		api.Post("/auth/logout", s.handleLogout)

		api.Post("/auth/passkey/register/options", s.handlePasskeyRegisterOptions)
		api.Post("/auth/passkey/register/verify", s.handlePasskeyRegisterVerify)
		api.Post("/auth/passkey/login/options", s.handlePasskeyLoginOptions)
		api.Post("/auth/passkey/login/verify", s.handlePasskeyLoginVerify)

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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
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
