package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/user/url-shortener/internal/model"
	"github.com/user/url-shortener/internal/service"
	"github.com/user/url-shortener/internal/worker"
)

const (
	maxUserAgentLen = 512
	maxRefererLen   = 2048
)

type Handler struct {
	auth        *service.AuthService
	shortener   *service.ShortenerService
	batcher     *worker.ClickBatcher
	limiter     RateLimiter
	ipExtractor *IPExtractor
	rlLimit     int
	rlWindow    time.Duration
}

func NewHandler(
	auth *service.AuthService,
	shortener *service.ShortenerService,
	batcher *worker.ClickBatcher,
	limiter RateLimiter,
	rlLimit int,
	rlWindow time.Duration,
	trustedProxies []string,
) *Handler {
	return &Handler{
		auth:        auth,
		shortener:   shortener,
		batcher:     batcher,
		limiter:     limiter,
		ipExtractor: NewIPExtractor(trustedProxies),
		rlLimit:     rlLimit,
		rlWindow:    rlWindow,
	}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(RateLimitMiddleware(h.limiter, h.ipExtractor, h.rlLimit, h.rlWindow))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/register", h.register)
		r.Post("/auth/login", h.login)

		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(h.auth))
			r.Post("/links", h.createLink)
			r.Get("/links", h.listLinks)
			r.Get("/links/{code}/stats", h.getStats)
			r.Delete("/links/{code}", h.deleteLink)
		})
	})

	r.Get("/{code}", h.redirect)

	return r
}

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	token, err := h.auth.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrValidation):
			writeError(w, http.StatusBadRequest, "invalid email or password (min 6 chars)")
		case errors.Is(err, model.ErrAlreadyExists):
			writeError(w, http.StatusConflict, "email already registered")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{Token: token})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	token, err := h.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, model.ErrInvalidCreds) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token})
}

type createLinkRequest struct {
	URL         string `json:"url"`
	CustomAlias string `json:"custom_alias"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

func (h *Handler) createLink(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	link, err := h.shortener.Create(r.Context(), service.CreateLinkInput{
		UserID:      getUserID(r.Context()),
		OriginalURL: req.URL,
		CustomAlias: req.CustomAlias,
		TTLSeconds:  req.TTLSeconds,
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrValidation):
			writeError(w, http.StatusBadRequest, "invalid url or custom alias")
		case errors.Is(err, model.ErrAlreadyExists):
			writeError(w, http.StatusConflict, "short code already in use")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	writeJSON(w, http.StatusCreated, link)
}

func (h *Handler) listLinks(w http.ResponseWriter, r *http.Request) {
	links, err := h.shortener.ListUserLinks(r.Context(), getUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if links == nil {
		links = []model.Link{}
	}
	writeJSON(w, http.StatusOK, links)
}

func (h *Handler) getStats(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	stats, err := h.shortener.GetStats(r.Context(), code, getUserID(r.Context()))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			writeError(w, http.StatusNotFound, "link not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) deleteLink(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	err := h.shortener.Delete(r.Context(), code, getUserID(r.Context()))
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			writeError(w, http.StatusNotFound, "link not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	targetURL, err := h.shortener.Resolve(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrNotFound):
			writeError(w, http.StatusNotFound, "link not found")
		case errors.Is(err, model.ErrLinkExpired):
			writeError(w, http.StatusGone, "link has expired")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	h.batcher.Enqueue(model.ClickEvent{
		ShortCode: code,
		IP:        h.ipExtractor.Extract(r),
		UserAgent: sanitizeString(r.UserAgent(), maxUserAgentLen),
		Referer:   sanitizeString(r.Referer(), maxRefererLen),
		ClickedAt: time.Now().UTC(),
	})

	http.Redirect(w, r, targetURL, http.StatusFound)
}
