# mcp-confluence

MCP-сервер для Confluence Data Center (`https://confluence.example.com`, REST API v1), через который Claude читает и пишет страницы только внутри поддеревьев, перечисленных в конфиге. Страницы за пределами разрешённых поддеревьев для агента неотличимы от несуществующих: он не получает ни их содержимого, ни названий, ни факта существования. Единственное известное исключение — «Остаточная утечка: занятый заголовок». Удаление и перенос страниц, комментирование, метки, права, пространства, пользователи и любые другие вызовы API невозможны.

## Зоны

- `read_roots` и `write_roots` — id страниц-корней. Поддерево корня — сам корень и все потомки на любую глубину.
- Зона чтения — поддеревья `read_roots` и `write_roots` вместе: всё, что можно писать, можно и читать.
- Зона записи — поддеревья `write_roots`, включая сами корни.
- Страница в зоне — объект с `type: page` и `status: current`, у которого сам id или id одного из предков — корень зоны. Блог-посты, черновики и страницы в корзине в зону не входят.
- Страница вне зоны, несуществующая страница и страница, недоступная токену (403, 404), дают агенту одну и ту же ошибку `page not found`.
- Страница не может покинуть зону записи или попасть в неё: перенос запрещён, создание — только под родителем из зоны записи.

## Что агент получает и чего не получает

Агенту возвращаются только поля из белого списка:

| Объект | Поля |
|---|---|
| страница (`get_page`) | `id`, `title`, `version`, `parent_id`, `webui` (абсолютная ссылка), `body` (storage) |
| записанная страница (`create_page`, `update_page`) | `id`, `title`, `version`, `webui` |
| ребёнок, результат поиска | `id`, `title` |
| комментарий | `id`, `parent_comment_id` (у ответа на комментарий), `author` (отображаемое имя), `created`, `body` (storage) |
| вложение | `id` (как отдаёт Confluence, например `att3604484`), `title`, `media_type`, `size`, `version` |

Пространство, предки, история, метки и ограничения страниц не отдаются. Ссылка `webui` содержит ключ пространства самой страницы; страница при этом в зоне, так что это не сведения о том, что лежит вне зоны. Тела ответов и ошибок Confluence агенту не передаются никогда — только ошибки сервера из раздела «Ошибки».

- `parent_id` отдаётся, только если родитель в зоне чтения; для этого `get_page` проверяет родителя запросом принадлежности (кроме случая, когда родитель сам корень зоны). У корня зоны `parent_id` нет, если его родитель вне зоны чтения.
- `search` перед выдачей перепроверяет каждый результат запросом принадлежности зоне. Не прошедшие проверку результаты и результаты с id не в форме id страницы молча отбрасываются, поэтому результатов может быть меньше `limit`.
- Отрендеренное тело (`body.view`) не отдаётся: в нём раскрываются макросы `include`, `excerpt-include`, `children`, `pagetree`, `jira` с содержимым чужих страниц. Ссылки `ri:page`, `ri:attachment`, `ri:user` в `body.storage` отдаются как написаны, сервер по ним ничего не запрашивает.

### Остаточная утечка: занятый заголовок

Название страницы уникально внутри пространства. Отказ `create_page` или `update_page` с ошибкой `this title is already taken in the space` означает, что где-то в пространстве — возможно, вне зоны — есть страница с таким названием. Это один бит на вызов (есть ли в пространстве страница с точно таким названием), без содержимого и местоположения. Другого известного способа узнать что-либо о страницах вне зоны через сервер нет.

### Токен

Токен пользовательский (PAT), запросы идут с вашими правами. Гарантии относятся к тому, что делается через MCP: агент с Bash может прочитать токен в режимах `env` и `file` и обратиться к Confluence в обход сервера. В режиме `keychain` токен без пароля связки читает только установленный бинарь `mcp-confluence` — см. «Хранение токена».

## Гарантии

Каждый исходящий HTTP-запрос сервера проходит через guard (`http.RoundTripper`). Guard включён всегда, флага отключения нет. Он пропускает только запросы из таблицы ниже, только для включённых инструментов и только к страницам нужной зоны. Принадлежность зоне guard проверяет перед каждым запросом сам, собственным запросом `GET /rest/api/content/{id}?expand=ancestors,space,container`; ответ проверки агенту не передаётся, кеша нет. Ошибка в коде выше транспорта не может отправить запрос к странице вне зоны.

| Запрос | Зона | Каким инструментам нужен |
|---|---|---|
| `GET /rest/api/content/{id}?expand=…`, `expand` из `body.storage`, `version`, `ancestors`, `space` | чтения | `get_page`, `search` (перепроверка результатов), `create_page` (пространство родителя) |
| `GET /rest/api/content/{id}/child/page?start&limit` | чтения | `list_children` |
| `GET /rest/api/content/search?cql&limit` — CQL строит сервер из корней конфига, guard сверяет его с конфигом | — | `search` |
| `GET /rest/api/content/{id}/child/comment?expand&depth=all&start&limit` | чтения | `list_comments` |
| `GET /rest/api/content/{id}/child/attachment?start&limit`, необязательный `expand=version` (другие значения `expand` запрещены) | чтения | `list_attachments`, `get_attachment`, `upload_attachment` |
| `GET /download/attachments/{id}/{name}?version&api=v2` | чтения | `get_attachment` |
| `POST /rest/api/content` — тело ровно `{type, title, space{key}, ancestors[{id}], body{storage}}`, `space.key` — пространство родителя | записи (родитель) | `create_page` |
| `PUT /rest/api/content/{id}` — тело ровно `{id, type, title, version{number}, body{storage}}` | записи | `update_page` |
| `POST /rest/api/content/{id}/child/attachment` и `…/{att}/data` (`{att}` — числовая часть id вложения этой страницы, без префикса `att`) — один файл в multipart | записи | `upload_attachment` |

Поиск: `(id = R1 or ancestor = R1 or …) and type = page and text ~ "<запрос>"` по всем корням зоны чтения. В запросе `\` и `"` экранируются, переводы строк и управляющие символы запрещены.

Ответы читаются с пределом: страница и списки — 8 MiB, вложение — `attachments.max_bytes`. Превышение — ошибка, частичные данные не отдаются.

## Имена вложений

Одно правило имени для скачивания и загрузки: имя непустое, не `.` и не `..`, без символов `/`, `\`, `;`, `"`, без управляющих и невидимых форматирующих символов Unicode (категория Cf). Вложение с другим именем через MCP не скачать (`tools: the attachment title is not a safe file name …`), файл с другим именем не загрузить (`tools: path must be …`).

Скачивание (`get_attachment`):

- файл сохраняется в `attachments.download_dir` под именем вложения; имя, начинающееся с точки, получает префикс `_` (`.zshrc` → `_.zshrc`);
- одноимённый файл перезаписывается; файл пишется во временный и переименовывается, права `0600`; симлинк на месте файла не разыменовывается;
- файл конфига, файл токена и связку ключей скачивание не перезаписывает ни по какому пути (`tools: the file is the config file, the token file or the keychain`);
- `download_dir` создаётся с правами `0700` при первом сохранении; права уже существующего каталога не меняются;
- вложение больше `max_bytes` не сохраняется.

Загрузка (`upload_attachment`):

- только обычный файл из каталогов `attachments.upload_dirs` (симлинки раскрываются; путь не должен проходить через домашний каталог или корень);
- расширение из `attachments.extensions` (регистр не важен), размер не больше `max_bytes`;
- файл конфига, файл токена и связку ключей сервер не отправляет ни по какому пути;
- имя вложения — имя файла после раскрытия симлинков; если у страницы уже есть вложение с таким именем, загружается его новая версия, а не второй файл.

## Установка и настройка

Пошагово — от выбора корней до первого вызова на живом Confluence — в [SETUP.md](SETUP.md).

### Запуск

```
mcp-confluence [--config path] serve
mcp-confluence [--config path] token
mcp-confluence --help
```

Флаг `--config` пишется до подкоманды. Любая ошибка печатается в stderr с префиксом `mcp-confluence:`, код выхода 1; `--help` — код 0.

- `serve` — MCP-сервер на stdio, его запускает Claude Code. При старте к Confluence не обращается. Не стартует, если конфиг невалиден или токен не получен. Во время работы пишет в stderr метод, путь и статус неудачных запросов, без тел ответов и без токена.
- `token` — записывает PAT в хранилище режима `secrets`:
  - в режимах `file` и `keychain` спрашивает токен, ввод без эха; в `keychain` при необходимости создаёт связку;
  - ввод без эха из терминала есть только на macOS, на других ОС токен подаётся в stdin через канал;
  - Ctrl+C отменяет ввод: токен не сохраняется, эхо терминала восстанавливается, код выхода 1;
  - в режиме `env` — ошибка `token: secrets mode is env, set MCP_CONFLUENCE_TOKEN in the environment instead`.

## Конфиг

Путь задаёт `--config`, по умолчанию `~/.config/mcp-confluence/config.yaml`. Начальный `~` раскрывается в домашний каталог в `--config` и в ключах `secrets_dir`, `keychain_path`, `attachments.download_dir`, `attachments.upload_dirs`. Конфиг читается при старте `serve`; после правки — `/mcp` → `scoped-confluence` → Reconnect.

```yaml
base_url: https://confluence.example.com
secrets: keychain
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db   # по умолчанию

read_roots: [123456789, 123456790]
write_roots: [987654321]

tools:
  get_page: true
  list_children: true
  search: true
  list_comments: true
  list_attachments: true
  get_attachment: true
  create_page: true
  update_page: true
  upload_attachment: true

attachments:
  download_dir: ~/agent-out/confluence
  upload_dirs: [~/agent-out]
  extensions: [.png, .svg, .drawio, .md, .txt, .json, .pdf]
  max_bytes: 10485760

search:
  max_results: 50
```

Неизвестный ключ в любом месте конфига — ошибка.

- `base_url` — абсолютный `https`-URL без userinfo, query и fragment; завершающий `/` отбрасывается.
- `secrets` — `env` (по умолчанию), `file` или `keychain` (только macOS); `secrets_dir` и `keychain_path` — см. «Хранение токена».
- `read_roots`, `write_roots` — id страниц: положительные числа, без повторов внутри списков и между ними. Названия не используются: название можно поменять, id — нет. Id страницы — в URL `…/pages/viewpage.action?pageId=…` или в «Информации о странице».
- `tools` — флаги инструментов, по умолчанию все выключены, хотя бы один обязателен. Инструменты записи (`create_page`, `update_page`, `upload_attachment`) требуют непустой `write_roots`, инструменты чтения — хотя бы один корень в любом из списков. Выключенный инструмент не регистрируется в MCP.
- `attachments` — задаётся ровно тогда, когда включён `get_attachment` или `upload_attachment`.
  - `download_dir` — куда `get_attachment` сохраняет файлы;
  - `upload_dirs` — непустой список каталогов, откуда `upload_attachment` берёт файлы;
  - `extensions` — непустой список расширений вида `.md`, без повторов без учёта регистра;
  - `max_bytes` — предел размера и загружаемого, и скачиваемого вложения, 1..20971520 (20 MiB).

  Ни `download_dir`, ни каталоги `upload_dirs` не могут быть домашним каталогом, его предком или корнем, в том числе через симлинк.
- `search.max_results` — верхняя граница и значение по умолчанию `limit` у `search`, 1..100, по умолчанию 25.

## Инструменты

| Инструмент | Зона | Вход | Выход |
|---|---|---|---|
| `get_page` | чтения | `page_id` | `id`, `title`, `version`, `parent_id` (если родитель в зоне), `webui`, `body` |
| `list_children` | чтения | `page_id`, `start` (по умолчанию 0), `limit` (1..100, по умолчанию 25) | `children[{id, title}]`, `has_more` |
| `search` | чтения | `query`, `limit` (1..`search.max_results`, по умолчанию `search.max_results`) | `results[{id, title}]` |
| `list_comments` | чтения | `page_id` | `comments[{id, parent_comment_id, author, created, body}]`, все уровни вложенности |
| `list_attachments` | чтения | `page_id` | `attachments[{id, title, media_type, size, version}]` |
| `get_attachment` | чтения | `page_id`, `attachment_id` (из `list_attachments`, с префиксом `att` или без) | `path` сохранённого файла |
| `create_page` | записи (родитель) | `parent_id`, `title`, `body` (storage) | `id`, `title`, `version`, `webui` |
| `update_page` | записи | `page_id`, `version` (прочитанная через `get_page`), `title`, `body` (storage, заменяет тело целиком) | `id`, `title`, `version`, `webui` |
| `upload_attachment` | записи | `page_id`, `path` (абсолютный или от `~/`) | `id`, `title`, `version` |

`list_comments` и `list_attachments` читают список пачками по 100; список длиннее 20 пачек (2000 элементов) даёт `Confluence is unavailable, retry later`. `get_attachment` и `upload_attachment` сначала читают список вложений страницы:

- `get_attachment`: вложения с таким id у этой страницы нет — `page not found`; id сравниваются по числовой части, `att501` и `501` — одно вложение;
- `upload_attachment`: одноимённого вложения нет — создаётся новое вложение, есть — его новая версия.

## Ошибки

Агент получает одну из ошибок сервера; тексты и тела ответов Confluence не передаются:

| Текст | Когда |
|---|---|
| `page not found` | страница вне зоны, не существует или недоступна токену (403, 404); вложения нет у страницы |
| `the page was changed since it was read; read it again` | 409 на `update_page` — версия устарела |
| `this title is already taken in the space` | 400 на `create_page`/`update_page`, в ответе признак занятого заголовка |
| `Confluence rejected the request` | прочие 4xx, на чтение и на запись (например, 400 на CQL, 409 на создание) |
| `the change may have been applied; check the page before retrying` | 5xx, сеть, таймаут (60 с) или неожиданный ответ на запись |
| `Confluence is unavailable, retry later` | 5xx, сеть, таймаут или неожиданный ответ на чтение; сбой самой проверки зоны (запрос не отправлялся, повтор безопасен) |
| `the token was rejected` | 401 |
| `the attachment is larger than attachments.max_bytes` | скачиваемое вложение больше `max_bytes` |
| `request is not allowed` | guard отказал по форме запроса — дефект сервера, не действие агента |

Ошибки входных данных и политики вложений начинаются с `tools:` и описывают, что не так с аргументом или файлом.

## Хранение токена

Через общую библиотеку `pkg/secret` монорепозитория scoped-mcp, имя секрета `token`:

| `secrets` | Где лежит токен | Ключ пути (по умолчанию) | Как записать |
|---|---|---|---|
| `env` (по умолчанию) | переменная `MCP_CONFLUENCE_TOKEN` | — | в окружении Claude Code |
| `file` | файл `token` в `secrets_dir`: каталог `0700`, файл `0600`; доступный группе или остальным не читается | `secrets_dir` (`~/.config/mcp-confluence/secrets`) | `mcp-confluence token` |
| `keychain` (macOS) | элемент с service `mcp-confluence` и account `token` в отдельной связке `keychain_path` | `keychain_path` (`~/Library/Keychains/scoped-mcp.keychain-db`) | `mcp-confluence token` |

Связка `scoped-mcp.keychain-db` общая с `mcp-tg`: один пароль, разные элементы. Если её нет, `mcp-confluence token` создаёт её и печатает `Creating keychain <путь>: choose its password in the macOS dialog`; пароль задаётся в системном диалоге и через процесс `mcp-confluence` не проходит.

macOS даёт доступ к элементу без диалога только бинарю, который его создал (по хешу подписи), и бинарям, разрешённым кнопкой «Разрешать всегда». Поэтому `mcp-confluence token` запускается уже установленным бинарём, а `make install-mcp-confluence` (из корня scoped-mcp):

- собирает только закоммиченное состояние всего репозитория scoped-mcp и останавливается на нечистом дереве;
- сверяет загруженные модули с `go.sum` (`go mod verify`);
- ставит с `GOFLAGS=-mod=readonly`: зависимости только из `go.mod` и `go.sum`;
- подписывает бинарь `codesign -s - -f -o runtime`: hardened runtime закрывает процесс от отладчика и `DYLD_INSERT_LIBRARIES`, а ACL элемента связки доверяет хешу именно подписанного бинаря.

После каждой пересборки первое обращение к элементу показывает диалог доступа к элементу связки — это ожидаемо сразу после своего `make install-mcp-confluence` и подозрительно в любой другой момент (см. [SETUP.md](SETUP.md), «Обновление mcp-confluence»).

## Разработка

Из корня scoped-mcp:

```bash
make generate                  # mockgen
make lint                      # golangci-lint v2 через go tool
make test
make build-mcp-confluence      # bin/mcp-confluence
make install-mcp-confluence    # только с чистого дерева scoped-mcp
```

`make generate`, `make lint` и `make test` проходят по всем серверам монорепозитория. Образцы структуры — соседние каталоги `mcp-jira` и `mcp-tg`. Тесты к живому Confluence не обращаются: ответы Confluence — фикстуры по документации Atlassian; поведение, которое по документации не подтвердить, проверяется вручную по [SETUP.md](SETUP.md), шаг 9.
