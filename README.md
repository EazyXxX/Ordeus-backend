# Ordeus Backend

Серверная часть личного органайзера Ordeus: API для дневника, календаря и канбана, фоновый разбор записей нейросетью. Интерфейс лежит в репозитории [Ordeus-frontend](https://github.com/EazyXxX/Ordeus-frontend).

## Стек

Go, `net/http`, PostgreSQL, pgx, goose, air.

## Запуск

Нужны Docker и Go. Сервер слушает `127.0.0.1:8080`.

```bash
docker compose up -d --wait
go tool air
```

Сервер сам пересобирается и перезапускается при изменении файлов `.go` и `.sql`. Миграции применяются при старте.

## Структура

```text
cmd/server/            точка входа
internal/ai/           клиент OpenAI-совместимых API (Ollama и внешние)
internal/api/          сборка HTTP-маршрутов
internal/auth/         аккаунты, сессии и личный кабинет
internal/calendar/     календарь: заметки и задачи со сроком
internal/config/       настройки из окружения
internal/database/     подключение к базе и миграции
internal/diary/        дневник: API, база и фоновый разбор записей
internal/httpjson/     общие помощники для JSON
internal/kanban/       канбан: API и работа с базой
```

## Новый модуль

Добавьте миграцию в `internal/database/migrations/`, создайте пакет в `internal/` и подключите его маршруты к `protected` в `internal/api/api.go`. Все таблицы модуля должны хранить `user_id`, а запросы фильтровать по `auth.UserID(ctx)`.

## Настройки сервера

| Переменная | По умолчанию |
|---|---|
| `ORDEUS_ADDR` | `127.0.0.1:8080` |
| `ORDEUS_DATABASE_URL` | `postgres://ordeus:ordeus@127.0.0.1:5432/ordeus?sslmode=disable` |
| `ORDEUS_SECURE_COOKIES` | `false`, включите при работе через HTTPS |
| `ORDEUS_AI_BASE_URL` | `http://127.0.0.1:11434/v1` |
| `ORDEUS_AI_MODEL` | `gemma4:12b`, пока модель не выбрана в приложении |
| `ORDEUS_AI_API_KEY` | не задан |
| `ORDEUS_SECRET_FILE` | `.ordeus-secret`, создаётся сам. Без него сохранённые ключи вендоров не расшифровать |
| `ORDEUS_AI_ALLOW_PRIVATE_URLS` | `false`, разрешает API-адреса во внутренней сети |
| `ORDEUS_AI_REASONING_EFFORT` | `none` для локальной Ollama, иначе не отправляется |

## Нейросеть в дневнике

Каждый аккаунт выбирает модель сам. Локальная работает через Ollama: `ORDEUS_AI_BASE_URL` задаёт адрес сервера, а `ORDEUS_AI_MODEL` — модель по умолчанию. Удалённая подключается в личном кабинете по адресу OpenAI-совместимого API, ключу и названию модели. Модель должна поддерживать ответ в JSON. Текст записей при этом уходит вендору. Ключ шифруется на сервере и не возвращается в браузер, а адрес должен быть публичным `https`.

Голос всегда распознаётся локальной моделью.
