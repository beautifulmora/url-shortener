# Сокращатель ссылок с аналитикой (Go)

Обычный, крепкий бэкенд для сокращения ссылок. Сделан без лишней магии, но со всем, что нужно в реальной жизни: кэширование редиректов в Redis, пачечное сохранение кликов в фоновом режиме (чтобы не душить базу на каждый переход), лимиты по IP и авторизация по JWT.

## Что под капотом

- **Роутинг:** `chi` — легковесный и полностью совместимый со стандартным `net/http`.
- **База данных:** PostgreSQL + `pgx/v5` (пул соединений, `CopyFrom` для быстрой вставки аналитики и `pgx.Batch` для обновления счетчиков).
- **Миграции:** накатываются автоматически при старте через `golang-migrate`.
- **Кэш и Rate Limiter:** Redis. Горячие ссылки отдаются напрямую из памяти, минуя Postgres. Если у ссылки задан TTL, в кэш она ложится с учетом оставшегося времени жизни.
- **Защита от спуфинга IP:** заголовкам `X-Forwarded-For` и `X-Real-IP` приложение доверяет только если запрос пришел от прокси из белого списка (`TRUSTED_PROXIES`, поддерживает IP и CIDR-подсети). По умолчанию берется честный `RemoteAddr`.
- **Аналитика кликов:** при редиректе клиент не ждет записи в БД — событие очищается от битого UTF-8/нулевых байтов, обрезается по длине, валидируется через `net.ParseIP` и падает в буферизированный канал. Фоновый воркер раз в 5 секунд (или при накоплении 100 штук) сбрасывает пачку в базу одной транзакцией с ретраями и учетом метрики потерянных событий (`dropped_events_total`). При остановке сервиса (`SIGTERM`/`SIGINT`) воркер аккуратно дописывает остатки из буфера.

## Как запустить

Проще всего поднять всё через Docker Compose — там уже настроены Postgres, Redis и само приложение:

```bash
docker-compose up --build
```

Сервис поднимется на `http://localhost:8080`.

Если хочется запустить локально без докера для самого Go-приложения:
1. Поднимите локально Postgres и Redis.
2. Скопируйте `.env.example` в `.env` (или экспортируйте переменные окружения).
3. Запустите:
```bash
go mod tidy
go run ./cmd/api/main.go
```

## Примеры запросов

### 1. Регистрация и вход

Регистрируемся и сразу получаем JWT-токен:

```bash
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email": "dev@example.com", "password": "secretpassword"}'
```

Ответ:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

Для повторного входа используется `/api/v1/auth/login` с тем же телом запроса.

### 2. Создание короткой ссылки

Можно передать просто URL, а можно указать свой алиас (`custom_alias`) и время жизни ссылки в секундах (`ttl_seconds`):

```bash
curl -X POST http://localhost:8080/api/v1/links \
  -H "Authorization: Bearer <ВАШ_ТОКЕН>" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://golang.org/doc/effective_go",
    "custom_alias": "eff-go",
    "ttl_seconds": 3600
  }'
```

Ответ:
```json
{
  "id": 1,
  "user_id": 1,
  "short_code": "eff-go",
  "short_url": "http://localhost:8080/eff-go",
  "original_url": "https://golang.org/doc/effective_go",
  "clicks_count": 0,
  "expires_at": "2026-10-24T22:00:00Z",
  "created_at": "2026-10-24T21:00:00Z"
}
```

Если `custom_alias` не указывать, сгенерируется случайный 7-символьный код.

### 3. Переход по ссылке (редирект)

Открываем в браузере или дергаем курлом:

```bash
curl -i http://localhost:8080/eff-go
```

Сервер ответит `302 Found` с заголовком `Location`. Если срок действия ссылки истёк, вернётся `410 Gone`.

### 4. Просмотр аналитики по ссылке

Посмотреть общую сумму переходов и последние 25 кликов (с IP, User-Agent и временем):

```bash
curl http://localhost:8080/api/v1/links/eff-go/stats \
  -H "Authorization: Bearer <ВАШ_ТОКЕН>"
```

*(Помните, что клики доезжают до базы пачками раз в 5 секунд или каждые 100 переходов).*

### 5. Список всех своих ссылок и удаление

Получить все созданные ссылки:
```bash
curl http://localhost:8080/api/v1/links \
  -H "Authorization: Bearer <ВАШ_ТОКЕН>"
```

Удалить ссылку (заодно сбрасывает кэш в Redis):
```bash
curl -X DELETE http://localhost:8080/api/v1/links/eff-go \
  -H "Authorization: Bearer <ВАШ_ТОКЕН>"
```

## Структура проекта

```text
├── cmd/api/main.go                  # Точка входа, сборка зависимостей и graceful shutdown
├── internal/
│   ├── config/                      # Чтение конфигурации из окружения
│   ├── model/                       # Доменные структуры и ошибки
│   ├── repository/
│   │   ├── postgres/                # Запросы к БД, миграции, батч-вставка через CopyFrom
│   │   └── redis/                   # Кэш редиректов и счетчик для Rate Limiter
│   ├── service/                     # Бизнес-логика (генерация кодов, JWT, проверка TTL)
│   ├── transport/http/              # Хэндлеры chi, middleware (Auth, RateLimit, IPExtractor)
│   └── worker/                      # Фоновый батчер кликов с ретраями и санитизацией
├── migrations/                      # SQL-миграции
├── Dockerfile
└── docker-compose.yml
```
