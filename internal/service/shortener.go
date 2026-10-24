package service

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/url"
	"time"

	"github.com/user/url-shortener/internal/model"
)

const (
	alphabet      = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	defaultCodeLn = 7
	defaultTTL    = 24 * time.Hour
)

type LinkRepository interface {
	Create(ctx context.Context, link *model.Link) error
	GetByCode(ctx context.Context, code string) (*model.Link, error)
	ListByUser(ctx context.Context, userID int64) ([]model.Link, error)
	Delete(ctx context.Context, code string, userID int64) error
	GetRecentClicks(ctx context.Context, code string, limit int) ([]model.ClickEvent, error)
}

type LinkCache interface {
	SetLink(ctx context.Context, code string, originalURL string, ttl time.Duration) error
	GetLink(ctx context.Context, code string) (string, error)
	DeleteLink(ctx context.Context, code string) error
}

type ShortenerService struct {
	links   LinkRepository
	cache   LinkCache
	baseURL string
}

func NewShortenerService(links LinkRepository, cache LinkCache, baseURL string) *ShortenerService {
	return &ShortenerService{
		links:   links,
		cache:   cache,
		baseURL: baseURL,
	}
}

type CreateLinkInput struct {
	UserID      int64
	OriginalURL string
	CustomAlias string
	TTLSeconds  int64
}

func (s *ShortenerService) Create(ctx context.Context, in CreateLinkInput) (*model.Link, error) {
	parsedURL, err := url.ParseRequestURI(in.OriginalURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return nil, model.ErrValidation
	}

	var expiresAt *time.Time
	cacheTTL := defaultTTL

	if in.TTLSeconds > 0 {
		exp := time.Now().Add(time.Duration(in.TTLSeconds) * time.Second)
		expiresAt = &exp
		if time.Duration(in.TTLSeconds)*time.Second < cacheTTL {
			cacheTTL = time.Duration(in.TTLSeconds) * time.Second
		}
	}

	code := in.CustomAlias
	if code != "" {
		if len(code) < 3 || len(code) > 32 || !isAlphanumeric(code) {
			return nil, model.ErrValidation
		}
		link := &model.Link{
			UserID:      in.UserID,
			ShortCode:   code,
			OriginalURL: in.OriginalURL,
			ExpiresAt:   expiresAt,
		}
		if err := s.links.Create(ctx, link); err != nil {
			return nil, err
		}
		link.ShortURL = s.baseURL + "/" + link.ShortCode
		_ = s.cache.SetLink(ctx, link.ShortCode, link.OriginalURL, cacheTTL)
		return link, nil
	}

	for i := 0; i < 5; i++ {
		generated, err := generateShortCode(defaultCodeLn)
		if err != nil {
			return nil, err
		}
		link := &model.Link{
			UserID:      in.UserID,
			ShortCode:   generated,
			OriginalURL: in.OriginalURL,
			ExpiresAt:   expiresAt,
		}
		err = s.links.Create(ctx, link)
		if err == nil {
			link.ShortURL = s.baseURL + "/" + link.ShortCode
			_ = s.cache.SetLink(ctx, link.ShortCode, link.OriginalURL, cacheTTL)
			return link, nil
		}
		if !errors.Is(err, model.ErrAlreadyExists) {
			return nil, err
		}
	}

	return nil, errors.New("failed to generate unique code")
}

func (s *ShortenerService) Resolve(ctx context.Context, code string) (string, error) {
	if cachedURL, err := s.cache.GetLink(ctx, code); err == nil && cachedURL != "" {
		return cachedURL, nil
	}

	link, err := s.links.GetByCode(ctx, code)
	if err != nil {
		return "", err
	}

	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		return "", model.ErrLinkExpired
	}

	cacheTTL := defaultTTL
	if link.ExpiresAt != nil {
		remaining := time.Until(*link.ExpiresAt)
		if remaining <= 0 {
			return "", model.ErrLinkExpired
		}
		if remaining < cacheTTL {
			cacheTTL = remaining
		}
	}

	_ = s.cache.SetLink(ctx, code, link.OriginalURL, cacheTTL)
	return link.OriginalURL, nil
}

func (s *ShortenerService) ListUserLinks(ctx context.Context, userID int64) ([]model.Link, error) {
	links, err := s.links.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range links {
		links[i].ShortURL = s.baseURL + "/" + links[i].ShortCode
	}
	return links, nil
}

func (s *ShortenerService) Delete(ctx context.Context, code string, userID int64) error {
	if err := s.links.Delete(ctx, code, userID); err != nil {
		return err
	}
	_ = s.cache.DeleteLink(ctx, code)
	return nil
}

func (s *ShortenerService) GetStats(ctx context.Context, code string, userID int64) (*model.LinkStats, error) {
	link, err := s.links.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if link.UserID != userID {
		return nil, model.ErrNotFound
	}
	link.ShortURL = s.baseURL + "/" + link.ShortCode

	recent, err := s.links.GetRecentClicks(ctx, code, 25)
	if err != nil {
		return nil, err
	}
	if recent == nil {
		recent = []model.ClickEvent{}
	}

	return &model.LinkStats{
		Link:         *link,
		RecentClicks: recent,
	}, nil
}

func generateShortCode(length int) (string, error) {
	b := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
