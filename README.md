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
internal/calendar/     календарь: заметки и задачи со сроком
internal/config/       настройки из окружения
internal/database/     подключение к базе и миграции
internal/diary/        дневник: API, база и фоновый разбор записей
internal/httpjson/     общие помощники для JSON
internal/kanban/       канбан: API и работа с базой
```

## Новый модуль

Добавьте миграцию в `internal/database/migrations/`, создайте пакет в `internal/` и подключите его маршруты в `internal/api/api.go`.

## Настройки сервера

| Переменная | По умолчанию |
|---|---|
| `ORDEUS_ADDR` | `127.0.0.1:8080` |
| `ORDEUS_DATABASE_URL` | `postgres://ordeus:ordeus@127.0.0.1:5432/ordeus?sslmode=disable` |
| `ORDEUS_AI_BASE_URL` | `http://127.0.0.1:11434/v1` |
| `ORDEUS_AI_MODEL` | `gemma4:12b`, пока модель не выбрана в приложении |
| `ORDEUS_AI_API_KEY` | не задан |
| `ORDEUS_AI_REASONING_EFFORT` | `none` для локальной Ollama, иначе не отправляется |

## Нейросеть в дневнике

По умолчанию записи разбирает локальная модель в Ollama, поэтому Ollama должна быть запущена, а модель скачана. Модель выбирается в дзен-режиме, в списке показаны все модели провайдера. Новая модель для Ollama появится там после `ollama pull <имя>`. Выбор хранится в базе и важнее `ORDEUS_AI_MODEL`.

Для внешнего провайдера с OpenAI-совместимым API задайте `ORDEUS_AI_BASE_URL`, `ORDEUS_AI_MODEL` и `ORDEUS_AI_API_KEY`. Модель должна поддерживать структурированный ответ `json_schema`. Текст записей при этом уходит этому провайдеру.
