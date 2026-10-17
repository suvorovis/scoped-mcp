# mcp-gitlab

MCP-сервер для GitLab (`https://gitlab.example.com`). Умеет четыре вещи с merge request:

- общий комментарий;
- комментарий к строке дифа (новая дискуссия);
- ответ в существующую дискуссию;
- черновик ревью.

Всё остальное невозможно: публикация черновиков, правка и удаление комментариев, resolve, approve, merge, правка MR, emoji, GraphQL и любые другие вызовы API. Каждый HTTP-запрос сервера проходит через guard (`http.RoundTripper`) — проверку на уровне транспорта, поэтому даже ошибка в остальном коде сервера не обойдёт эти ограничения.

Имена:

- `mcp-gitlab` — этот каталог монорепозитория scoped-mcp и бинарь;
- `scoped-gitlab` — имя этого сервера в `.mcp.json` в примерах ниже;
- `gitlab-readonly` — другой, read-only MCP для чтения MR, дифов и дискуссий при ревью. `mcp-gitlab` читает только то, что нужно ему самому, и агенту прочитанное не отдаёт.

## Установка и настройка

Пошагово — от выпуска персонального токена GitLab (PAT) до работы в Claude Code и ротации токена — в [SETUP.md](SETUP.md).

Кратко:

1. `make install-mcp-gitlab` из корня scoped-mcp.
2. Конфиг `~/.config/mcp-gitlab/config.yaml` с режимом хранения токена `secrets`: `keychain`, `file` или `env` (см. «Хранение токена»).
3. PAT со скоупом `api` и сроком жизни.
4. Токен — `mcp-gitlab token` (режимы `keychain` и `file`) или переменная `MCP_GITLAB_TOKEN` (режим `env`).
5. `mcp-gitlab serve` в `.mcp.json`.

### Запуск

Обычно сервер запускает Claude Code по записи в `.mcp.json` (SETUP.md, шаг 6). Вручную — для проверки:

```
mcp-gitlab [--config path] serve
mcp-gitlab [--config path] token
mcp-gitlab --help
```

Флаг `--config` пишется до подкоманды. Любая ошибка печатается в stderr с префиксом `mcp-gitlab: `, код выхода 1; `--help` — код 0.

- `serve` — MCP-сервер на stdio. Не стартует, если конфиг невалиден или токен не получен (см. «Хранение токена»). Guard включён всегда, флага отключения нет.
- `token` — записывает PAT в хранилище режима `secrets`:
  - в режимах `file` и `keychain` спрашивает токен (`GitLab personal access token: `); в `keychain` при необходимости создаёт связку;
  - ввод: на macOS из терминала — без эха; через канал (`… | mcp-gitlab token`) — на любой ОС; на другой ОС из терминала — ошибка `token: hiding terminal input is supported only on macOS, pipe the token into stdin instead`;
  - Ctrl+C отменяет ввод: токен не сохраняется, эхо терминала восстанавливается, код выхода 1;
  - в режиме `env` — ошибка `token: secrets mode is env, set MCP_GITLAB_TOKEN in the environment instead`.

`serve` завершается по концу stdin или по первому `SIGINT`/`SIGTERM` (код выхода 0). Если штатное завершение зависло, второй сигнал убивает процесс сразу.

## Конфиг

Путь к конфигу задаёт `--config` (по умолчанию `~/.config/mcp-gitlab/config.yaml`). Домашний каталог раскрывается так:

- в `--config` — `~` и `~/…`;
- в `secrets_dir` и `keychain_path` — только `~/…`.

Если домашний каталог не определён — `ErrHomeUnknown`.

```yaml
base_url: https://gitlab.example.com
secrets: keychain   # по умолчанию env
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db   # можно не указывать: это значение по умолчанию

tools:
  add_mr_note: true
  add_mr_line_comment: true
  reply_to_discussion: true
  add_draft_note: true
```

- `base_url`:
  - формат — `https://хост[:порт]`, один завершающий `/` допустим и отбрасывается;
  - запрещены userinfo (`user@`, `user:password@`), путь, query (даже пустой `?`) и fragment — `ErrBaseURL`;
  - если URL не разбирается вовсе, в ошибке только `not a parsable URL`, сам URL не повторяется;
  - сервер работает только с этим хостом, другие хосты GitLab не подключаются.
- `secrets` — `env` (по умолчанию), `file` или `keychain` (только macOS), иначе `ErrSecretsMode`; `secrets_dir` и `keychain_path` — см. «Хранение токена».
- `secrets_dir` и `keychain_path` — абсолютный путь или путь, начинающийся с `~/` (`ErrSecretsPath`); относительный и пустой путь, голый `~` и `~user/…` отклоняются, даже если хранилище не используется выбранным режимом.
- `tools` — какие инструменты включены, по умолчанию все `false`. Должен быть включён хотя бы один (`ErrNoTools`); без раздела `tools` конфиг не загружается. Выключенного инструмента нет в ответе `tools/list`, и guard не пропускает его запросы. Неизвестные ключи в любом разделе — `ErrMalformed`.

Проекты не ограничиваются: любой проект хоста `base_url`, адресуемый числовым id или путём `group/sub/project`.

### Хранение токена

Через общую библиотеку `pkg/secret` монорепозитория scoped-mcp, имя секрета `token`. Токен читается только из хранилища режима `secrets`, другие хранилища не проверяются. Пробельные символы по краям обрезаются.

| `secrets` | Где лежит токен | Ключ конфига с путём (значение по умолчанию) | Как записать |
|---|---|---|---|
| `env` (по умолчанию) | переменная `MCP_GITLAB_TOKEN` | — | в окружении Claude Code |
| `file` | файл `token` в `secrets_dir`: каталог `0700`, файл `0600`; доступный группе или остальным не читается | `secrets_dir` (`~/.config/mcp-gitlab/secrets`) | `mcp-gitlab token` |
| `keychain` (macOS) | элемент с service `mcp-gitlab` и account `token` в отдельной связке `keychain_path` | `keychain_path` (`~/Library/Keychains/scoped-mcp.keychain-db`) | `mcp-gitlab token` |

Связка `scoped-mcp.keychain-db` общая с другими MCP (`mcp-tg`, `mcp-confluence`): один пароль, разные элементы. Если её нет, `mcp-gitlab token` создаёт её и печатает `Creating keychain <путь>: choose its password in the macOS dialog`; пароль задаётся в системном диалоге и через процесс `mcp-gitlab` не проходит.

macOS даёт доступ к элементу без диалога только бинарю, который его создал (по хешу подписи), и бинарям, разрешённым кнопкой «Разрешать всегда». Поэтому `mcp-gitlab token` запускается уже установленным бинарём, а `make install-mcp-gitlab` (из корня scoped-mcp):

- собирает только закоммиченное состояние всего репозитория scoped-mcp и останавливается на нечистом дереве;
- сверяет загруженные модули с `go.sum` (`go mod verify`);
- ставит с `GOFLAGS=-mod=readonly` в `$(go env GOPATH)/bin`;
- подписывает бинарь `codesign -s - -f -o runtime`: hardened runtime закрывает процесс от отладчика и `DYLD_INSERT_LIBRARIES`, а ACL элемента связки доверяет хешу именно подписанного бинаря.

После каждой пересборки первое обращение к элементу показывает диалог доступа к элементу связки — это ожидаемо сразу после своего `make install-mcp-gitlab` и подозрительно в любой другой момент (см. [SETUP.md](SETUP.md), «Обновление mcp-gitlab»).

Ошибки конфига и токена — переменные `config.Err…` (проверяются через `errors.Is`); в stderr печатаются с префиксом `mcp-gitlab: `:

| Ошибка | Текст |
|---|---|
| `ErrMalformed` | `config: malformed YAML: <причина>` |
| `ErrBaseURL` | `config: base_url must be an absolute https URL without userinfo, path, query and fragment: …` |
| `ErrSecretsMode` | `config: secrets must be env, file or keychain: …` |
| `ErrSecretsPath` | `config: secrets_dir and keychain_path must be absolute or start with ~/: "<путь>"` |
| `ErrNoTools` | `config: tools must enable at least one tool` |
| `ErrHomeUnknown` | `config: home directory is unknown, ~ cannot be expanded: …` |
| `ErrTokenUnavailable` | `config: token is not available: <причина из хранилища>` или `config: token is not available: token is empty` |

## Инструменты

Общие параметры: `project` — числовой id или путь `group/sub/project`; `mr_iid` — целое ≥ 1, номер MR из его URL.

Схема (`additionalProperties: false`) отклоняет: лишние параметры; `mr_iid` и номера строк меньше 1, дробные и не числа.

Описания инструментов предупреждают модель:

- ни одна строка текста не должна начинаться с `/` (пути и команды — в обратных кавычках);
- комментарий публикуется сразу от имени владельца токена;
- черновики видит только владелец токена, публикует их пользователь.

| Инструмент | Флаг | Параметры | Ответ |
|---|---|---|---|
| `add_mr_note` | `tools.add_mr_note` | `body` | `note_id`, `url` |
| `add_mr_line_comment` | `tools.add_mr_line_comment` | `body`, `new_path`, `old_path` (по умолчанию = `new_path`); `new_line`/`old_line` — номер строки (у многострочного — последней), нужен хотя бы один; `start_new_line`/`start_old_line` — первая строка, только для многострочного | `discussion_id`, `note_id`, `url` |
| `reply_to_discussion` | `tools.reply_to_discussion` | `discussion_id` (40 символов hex в нижнем регистре), `body` | `note_id`, `url` |
| `add_draft_note` | `tools.add_draft_note` | `note`; необязательно либо `in_reply_to_discussion_id`, либо позиция (`new_path`, `old_path`, `new_line`, `old_line`, `start_new_line`, `start_old_line` — как у `add_mr_line_comment`) | `draft_note_id`, `url` (ссылка на MR) |

Ввод проверяется до любого запроса к GitLab (ошибки — переменные `tools.Err…`):

| Проверка | Ошибка |
|---|---|
| `project` — по тем же правилам, что в guard | `ErrProjectInvalid` |
| `mr_iid ≥ 1` | `ErrMRIIDInvalid` |
| текст непуст после обрезки пробелов; `new_path` у позиции задан | `ErrFieldRequired` |
| ни одна строка текста после обрезки ведущих пробельных символов не начинается с `/` | `ErrQuickAction` |
| `discussion_id` и `in_reply_to_discussion_id` — 40 символов hex в нижнем регистре | `ErrDiscussionIDInvalid` |
| `new_path` и `old_path` без управляющих символов | `ErrPathInvalid` |
| задан хотя бы один из `new_line`/`old_line` | `ErrLineRequired` |
| номер строки не отрицательный | `ErrLineInvalid` |
| у черновика не заданы одновременно `in_reply_to_discussion_id` и позиция | `ErrDraftTarget` |

Через MCP ноль и отрицательные номера раньше отсекает схема. Все ошибки, включая отказ схемы, возвращаются как результат `tools/call` с `isError: true`, не как ошибка протокола.

После проверки ввода:

- Каждый инструмент запрашивает MR (`GET …/merge_requests/<iid>`): так проверяется, что MR существует, берётся `web_url` для ссылки и `diff_refs` для позиции. SHA агент не передаёт.
- `url` опубликованного комментария — `<web_url MR>#note_<note_id>`.
- Позиция: строка добавлена — `new_line`; удалена — `old_line`; не изменена (контекст) — любой из двух или оба. GitLab 16.0.8 у неизменённой строки требует оба номера, с одним отвечает 400 `line_code can't be blank`. Поэтому, если у строки комментария (у многострочного — у последней) передан ровно один номер, сервер находит её в дифе файла и у неизменённой подставляет второй номер; у добавленной и удалённой номер уходит как есть. Однострочный комментарий с одним номером тоже скачивает диф файла и даёт те же ошибки до записи, что многострочный (ниже). Однострочный комментарий с обоими номерами сервер с дифом не сверяет: если строки в нём нет, GitLab отвечает 400, ошибка возвращается агенту как есть. У переименованного файла `old_path` обязателен. MR без `diff_refs` (без коммитов) — `ErrDiffRefsMissing` до записи; общий комментарий и ответ в дискуссию к такому MR работают.
- Многострочный комментарий — задан хотя бы один из `start_new_line`, `start_old_line`:
  - Как ищется диф файла: `GET …/diffs?page=N&per_page=20`, страница за страницей, до файла с этим `new_path`, до страницы короче 20 или до 150-й страницы. Хунки разбираются по правилам `Gitlab::Diff::Parser`, в них находятся первая и последняя строка.
  - Как собирается `line_range`: у каждого конца `line_code` = `sha1(new_path)_<old>_<new>`.
    У добавленной строки `<old>` — счётчик старой версии на момент строки, у удалённой `<new>` — счётчик новой.
  - `type`: `new` у добавленной строки, иначе `old`.
  - Номера строк у концов `line_range` не передаются, только `line_code` и `type`: GitLab 16.0.8 объявляет `line_range.*.old_line/new_line` в API строками (Grape `String`), а json schema позиции требует там целое — любой номер даёт 400 `must be a valid json schema`. Номера остаются в `old_line`/`new_line` позиции. Следствие в UI 16.0.8: диапазон строк в дифе подсвечивается, но заголовка «Comment on lines X to Y» нет — `showMultiLineComment` берёт номера только из концов `line_range`.
  - Ошибки — до записи:
    - строки нет в дифе (в том числе диф пуст) — `diff.ErrLineNotInDiff`; первая строка после последней — `diff.ErrLineRangeOrder`;
    - диф не разбирается (бинарный файл — `Binary files … differ`, текст до первого заголовка хунка, неверный заголовок хунка, диф из одних пробелов) — `ErrDiffUnreadable`;
    - файла нет в MR, диф файла `collapsed` или `too_large`, страница дифа больше 32 MiB — `gitlab.ErrDiffUnavailable`.
- Черновики агент не публикует: `bulk_publish` и публикация отдельного черновика заблокированы guard; публикует пользователь кнопкой **Submit review** в GitLab.

## Гарантии

Guard пропускает в нижний транспорт только запрос, у которого:

- адрес:
  - схема и хост совпадают с `base_url`;
  - заголовок `Host` пуст или равен хосту;
  - в URL нет userinfo и `Opaque`;
  - `RawPath` пуст или корректно кодирует `Path`.

  Путь разбирается только в escaped-форме: так `%2F` в `<project>` не дробит сегменты, а расхождение `RawPath` и `Path` не даёт GitLab увидеть другой путь;
- заголовки — только те, что ставит клиент: `PRIVATE-TOKEN`, `Accept`, `Content-Type`; имя сравнивается без учёта регистра, так что ключ в нестандартном написании (`sudo`) не проскакивает. Любой другой заголовок — отказ, например:
  - `X-HTTP-Method-Override`, `X-HTTP-Method`, `X-Method-Override` — GitLab выполнил бы разрешённый `POST` как `PUT` или `DELETE`;
  - `Sudo` — с токеном администратора запрос ушёл бы от имени другого пользователя;
  - `Content-Encoding` — GitLab или прокси перед ним могли бы раскодировать тело не так, как его проверил guard;
  - `X-Forwarded-For` и прочие `X-…`.

  `User-Agent`, `Accept-Encoding` и `Content-Length` добавляет `net/http` уже под guard, поэтому список их не содержит;
- нет трейлеров (`Trailer`) и `Transfer-Encoding` — заголовки после тела и иная разметка тела прошли бы мимо проверок выше;
- путь — `/api/v4/projects/<project>/merge_requests/<iid>[/<subresource>]`, где `<iid>` — `^[1-9][0-9]*$`, а `<project>` — либо `^[1-9][0-9]*$`, либо не меньше двух сегментов `^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`, разделённых `%2F` или `%2f`; сегменты `.` и `..` запрещены;
- метод и subresource образуют один из маршрутов, включённый своим флагом (флаги guard проверяет сам, не доверяя валидации конфига):

| Маршрут | Метод и путь | Тело | Когда пропускается |
|---|---|---|---|
| MR | `GET …/merge_requests/<iid>` | нет | любой флаг `tools` |
| diffs | `GET …/merge_requests/<iid>/diffs`, query — только `page` и `per_page` (целые ≥ 1, каждый не больше одного раза) | нет | `add_mr_line_comment` или `add_draft_note` |
| note | `POST …/merge_requests/<iid>/notes` | `{"body"}` | `add_mr_note` |
| discussion | `POST …/merge_requests/<iid>/discussions` | `{"body","position"}` | `add_mr_line_comment` |
| reply | `POST …/merge_requests/<iid>/discussions/<discussion_id>/notes`, `<discussion_id>` — `^[0-9a-f]{40}$` | `{"body"}` | `reply_to_discussion` |
| draft | `POST …/merge_requests/<iid>/draft_notes` | `{"note"}`, необязательно ровно одно из `position`, `in_reply_to_discussion_id` | `add_draft_note` |

Правила запроса и тела:

- `GET` — без тела (`nil` или `http.NoBody`), с пустым query (кроме `diffs`).
- `POST`:
  - query пуст;
  - заголовок `Content-Type` ровно один, `application/json` (допускается только `; charset=utf-8`) — иначе GitLab разобрал бы тело как форму в обход проверок ниже;
  - тело — один JSON-объект не больше 1 MiB, без повторов ключей (на любом уровне) и без данных после него;
  - тело — валидный UTF-8: `encoding/json` заменил бы битые байты на U+FFFD, а GitLab (Ruby) мог прочитать их иначе, чем проверил guard;
  - ключи — только из столбца «Тело»; любые другие (`internal`, `resolve_discussion`, `merge_request_diff_head_sha`, `commit_id`, `created_at` и прочие) — отказ.
- Текст (`body` или `note`) — строка, непустая после обрезки пробелов. Ни одна строка текста (разделитель `\n`, `\r` в конце строки отбрасывается) после обрезки ведущих пробельных символов не начинается с `/` — иначе `/merge`, `/approve`, `/close` в «просто комментарии» выполнились бы как quick actions (у черновика — при публикации).
- `position` — объект ровно с ключами `position_type`, `base_sha`, `start_sha`, `head_sha`, `old_path`, `new_path`, хотя бы одним из `old_line`, `new_line` и необязательным `line_range`; `position_type` = `"text"`; SHA — `^[0-9a-f]{40}$`; пути — непустые строки без управляющих символов; номера строк — JSON-числа по `^[1-9][0-9]*$`.
- `line_range` — объект ровно с ключами `start` и `end`; каждый — объект ровно с ключами `line_code` и `type` (`old_line`, `new_line` — отказ, см. выше про GitLab 16.0.8); `type` — `"new"` или `"old"`; `line_code` — `^[0-9a-f]{40}_[0-9]+_[0-9]+$`, где первая часть равна SHA1 от `new_path` позиции.
- `in_reply_to_discussion_id` — `^[0-9a-f]{40}$`.

Иначе — `guard.ErrForbidden`, нижний транспорт не вызывается. `http.Client` дописывает перед текстом метод и URL: `Post "https://gitlab.example.com/api/v4/…": guard: forbidden: <причина>`. Отправляется ровно то тело, которое проверил guard, в том числе при повторной отправке внутри `net/http`; исходное тело всегда закрывается.

Клиент:

- заголовки: `PRIVATE-TOKEN`, `Accept: application/json`, у `POST` — `Content-Type: application/json`;
- `<project>` кодируется `url.PathEscape` (`/` → `%2F`), URL собирается с явным `RawPath`;
- редиректы не выполняются (`CheckRedirect` → `http.ErrUseLastResponse`);
- таймаут 30 с, повторов нет;
- ответ читается не больше 1 MiB, страница `diffs` — не больше 32 MiB; больше — `gitlab.ErrResponseTooLarge`.

## Ошибки

Ошибки GitLab — переменные `gitlab.Err…` и тип `*gitlab.StatusError`. Часть ошибок — обёртки (`ErrMergeRequestUnavailable`, `ErrDiffUnavailable`, `ErrCommentUnconfirmed`): их текст идёт первым, причина — после `: `.

| Ситуация | Результат |
|---|---|
| отказ guard | `<метод> "<URL>": guard: forbidden: …`, запрос не отправлялся; ничем не обёрнута. При работе через инструменты этого быть не должно — ошибка в сервере |
| `401` | `ErrUnauthorized`: `gitlab: 401 Unauthorized: the token has expired or was revoked; issue a new personal access token with the api scope and save it with mcp-gitlab token or set MCP_GITLAB_TOKEN` |
| `3xx`/`4xx`, кроме `401` | `StatusError`: `gitlab: <код> <статус>` и текст из `message` или `error` ответа GitLab (`404` — нет MR, проекта, дискуссии или прав; `400` — GitLab отклонил ввод, например строки нет в дифе) |
| тело успешного ответа больше 1 MiB (страница `diffs` — больше 32 MiB) | `ErrResponseTooLarge`: `gitlab: the response exceeds the size limit: more than <N> bytes`; приходит внутри `ErrMergeRequestUnavailable`, `ErrDiffUnavailable` или `ErrCommentUnconfirmed`. У ответа с ошибкой слишком большое тело не разбирается: `StatusError` без текста GitLab |
| ошибка запроса MR (сеть, не `200`, в том числе `401` и `404`, не JSON, нет `web_url`, слишком большой ответ) | `ErrMergeRequestUnavailable` вместе с причиной: `gitlab: the merge request could not be fetched; nothing was posted, it is safe to retry` |
| ошибка получения дифа файла (сеть, не `200`, файла нет в MR или он дальше 150-й страницы, `collapsed`, `too_large`, слишком большая страница) | `ErrDiffUnavailable` вместе с причиной: `gitlab: the file diff could not be fetched; nothing was posted, it is safe to retry` |
| MR без `diff_refs` у комментария к строке или черновика с позицией | `tools.ErrDiffRefsMissing`: `tools: the merge request has no diff_refs (no commits yet?), a line position cannot be built; nothing was posted, retry after the merge request has commits` |
| диф файла не разбирается | `tools.ErrDiffUnreadable`: `tools: the file diff could not be parsed (binary file?), the line cannot be located in it; nothing was posted` |
| отправка записи: `5xx` (вместе со `StatusError`), сеть, таймаут, `2xx` без id, не JSON, слишком большое или нечитаемое тело ответа | `ErrCommentUnconfirmed`: `gitlab: the comment was sent but GitLab did not confirm it; it may have been added, check the merge request before retrying`. Сервер сам не повторяет; GitLab не отсеивает повторы |

Каждый инструмент сначала читает MR, поэтому истёкший токен обычно виден как `ErrMergeRequestUnavailable` с причиной `ErrUnauthorized`.

Токен не попадает в ответы, описания инструментов и stderr; в ошибках токена печатается только причина из хранилища (имя переменной, путь к файлу или ошибка связки), без значения. В `serve` stdout занят протоколом MCP; в stderr попадает только ошибка, с которой `serve` завершился.

Гарантии не защищают от агента, который сам прочитает токен и обратится к GitLab в обход сервера — см. [SETUP.md, «Ограничение: агент может прочитать токен»](SETUP.md#ограничение-агент-может-прочитать-токен).

## Ручная проверка

Для проверки без GitLab годится любой непустой токен: `tools/list` и отказы по вводу в сеть не ходят. Фиктивный токен передаётся переменной, поэтому проверка идёт на временном конфиге в режиме `env`. Из корня scoped-mcp:

```bash
make build-mcp-gitlab
CONFIG=$(mktemp)
printf '%s\n' 'base_url: https://gitlab.example.com' 'secrets: env' \
  'tools: {add_mr_note: true, add_mr_line_comment: true, reply_to_discussion: true, add_draft_note: true}' > "$CONFIG"
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_mr_note","arguments":{"project":"group/ci-test","mr_iid":1,"body":"/close"}}}'
  sleep 3; } | MCP_GITLAB_TOKEN=fake ./bin/mcp-gitlab --config "$CONFIG" serve
rm -f "$CONFIG"
```

`sleep` держит stdin открытым: на конце ввода MCP SDK закрывает соединение, и ответы на ещё не обработанные запросы теряются.

Ожидается: `"id":2` — инструменты, включённые в `tools` (при этом временном конфиге — все четыре: `add_draft_note`, `add_mr_line_comment`, `add_mr_note`, `reply_to_discussion`); `"id":3` — `isError: true`, текст `tools: no line of the text may start with /…`. Ответы могут прийти не по порядку `id`. В GitLab ничего не создаётся. Если вместо ответов сразу пришла строка в stderr — это ошибка конфига или токена.

Проверка с настоящим токеном на MR в статусе Draft — [SETUP.md, шаг 7](SETUP.md#7-проверить-в-сессии).

## Разработка

Из корня scoped-mcp:

```bash
make generate              # mockgen
make lint                  # golangci-lint v2 через go tool (включает gofumpt и goimports)
make test
make build-mcp-gitlab      # bin/mcp-gitlab
make install-mcp-gitlab    # только с чистого дерева scoped-mcp
```

`make generate`, `make lint` и `make test` проходят по всем серверам монорепозитория.
