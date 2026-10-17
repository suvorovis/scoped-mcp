# scoped-mcp

MCP-серверы для Claude Code. Каждый сервер даёт агенту узкий набор действий во внешней системе, а guard на уровне транспорта не пропускает остальные запросы.

| Сервер | Назначение | Документация |
|---|---|---|
| `mcp-tg` | Read-only доступ к Telegram: сообщения только из чатов и папок, перечисленных в конфиге. | [README](mcp-tg/README.md), [SETUP](mcp-tg/SETUP.md) |
| `mcp-jira` | Jira Data Center: создание задач в проектах и типах из конфига, вложения из разрешённых каталогов, комментарии, связи, изменение полей из конфига (перенос между эпиками). | [README](mcp-jira/README.md), [SETUP](mcp-jira/SETUP.md) |
| `mcp-gitlab` | GitLab: комментарии к merge request — общий, к строке дифа, ответ в дискуссию, черновик ревью. | [README](mcp-gitlab/README.md), [SETUP](mcp-gitlab/SETUP.md) |
| `mcp-confluence` | Confluence Data Center: чтение и запись страниц только внутри поддеревьев из конфига. | [README](mcp-confluence/README.md), [SETUP](mcp-confluence/SETUP.md) |

## Структура

```
go.mod, go.sum      один Go-модуль scoped-mcp на все серверы
.golangci.yml       общий конфиг линтера
Makefile            сборка, проверки и установка всех серверов
pkg/secret/         общая библиотека хранения секретов
mcp-<сервер>/
  cmd/mcp-<сервер>/ точка входа бинаря
  …                 пакеты сервера; README.md и SETUP.md
```

## pkg/secret

Хранилище секретов для всех серверов — `mcp-tg`, `mcp-jira`, `mcp-gitlab` и `mcp-confluence` (импорт `scoped-mcp/pkg/secret`), режим выбирается ключом `secrets` в конфиге сервера:

- `env` — переменная окружения;
- `file` — файл в каталоге `0700`, файл `0600`;
- `keychain` (только macOS) — отдельная связка `~/Library/Keychains/scoped-mcp.keychain-db`, общая для всех серверов: один пароль, элементы различаются атрибутом service.

## Makefile

Все команды — из корня репозитория.

| Цель | Что делает |
|---|---|
| `make generate` | `go generate ./...` (mockgen) |
| `make lint` | golangci-lint v2 через `go tool` |
| `make test` | `go test ./...` |
| `make build` | собирает все серверы в `bin/<сервер>` |
| `make build-<сервер>` | собирает один сервер, например `make build-mcp-tg` |
| `make install` | `install-<сервер>` для всех серверов |
| `make install-<сервер>` | ставит сервер в `$(go env GOPATH)/bin` и подписывает |

`install-<сервер>`:

- останавливается на нечистом дереве всего репозитория с `рабочее дерево не чистое: install только из закоммиченного состояния`;
- сверяет загруженные модули с `go.sum` (`go mod verify`);
- ставит с `GOFLAGS=-mod=readonly`;
- подписывает бинарь `codesign -s - -f -o runtime`: hardened runtime и новый хеш подписи, поэтому после установки серверы в режиме `keychain` показывают диалог доступа к элементу связки (подробно — в README и SETUP сервера).

Имена бинарей, подкоманды и конфиги `~/.config/<сервер>/…` у каждого сервера свои; установка и настройка — в его SETUP.md.
