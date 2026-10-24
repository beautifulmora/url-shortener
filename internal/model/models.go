package model

import (
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("resource not found")
	ErrLinkExpired       = errors.New("link has expired")
	ErrAlreadyExists     = errors.New("resource already exists")
	ErrInvalidCreds      = errors.New("invalid credentials")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrValidation        = errors.New("validation error")
)

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Link struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"user_id"`
	ShortCode   string     `json:"short_code"`
	ShortURL    string     `json:"short_url,omitempty"`
	OriginalURL string     `json:"original_url"`
	ClicksCount int64      `json:"clicks_count"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ClickEvent struct {
	ShortCode string    `json:"short_code"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Referer   string    `json:"referer"`
	ClickedAt time.Time `json:"clicked_at"`
}

type LinkStats struct {
	Link         Link         `json:"link"`
	RecentClicks []ClickEvent `json:"recent_clicks"`
}
