# mcp-jira

MCP-сервер для Jira Data Center (`https://jira.example.com`), умеющий пять вещей: создавать задачи в проектах и типах из конфига с полями, разрешёнными для типа, прикладывать к задачам этих проектов файлы из разрешённых каталогов, добавлять к ним комментарии, связывать задачи этих проектов отношениями из конфига и менять у них поля из `update_fields` проекта (например, переносить задачу в другой эпик или убирать из эпика). Чтение, поиск, правка других полей и удаление (в том числе комментариев и связей), переходы, связи с типом вне `issue_links` и любые другие вызовы API невозможны; какая задача окажется на какой стороне связи, guard не сверяет (см. абзац о сторонах связи в разделе guard).

Каждый исходящий HTTP-запрос проходит через guard (`http.RoundTripper`), который пропускает только:

- `POST <base_url>/rest/api/2/issue` с проверенным телом — создание задачи, только при `tools.create_issue: true`;
- `POST <base_url>/rest/api/2/issue/<KEY>/attachments` с одним проверенным файлом — только при `tools.attach_files: true` и разделе `attachments`;
- `POST <base_url>/rest/api/2/issue/<KEY>/comment` с телом `{"body": "<текст>"}` — только при `tools.add_comment: true`;
- `POST <base_url>/rest/api/2/issueLink` с телом ровно `{"type":{"name":…},"inwardIssue":{"key":…},"outwardIssue":{"key":…}}` — только при `tools.link_issues: true` и непустом `issue_links`;
- `PUT <base_url>/rest/api/2/issue/<KEY>` с телом ровно `{"fields": {…}}`, где ключи `fields` — ID полей Jira из `update_fields` проекта задачи, — только при `tools.update_issue: true`;
- `GET <base_url>/rest/api/2/myself` без тела — владелец токена, только при `tools.create_issue: true` и `assign_to_self` хотя бы у одного проекта.

## Установка и настройка

Пошагово — от выпуска персонального токена Jira (PAT) до работы в Claude Code и ротации токена — в [SETUP.md](SETUP.md).

Кратко:

1. `make install-mcp-jira` из корня scoped-mcp.
2. Конфиг `~/.config/mcp-jira/config.yaml` с режимом хранения токена `secrets`: `keychain`, `file` или `env` (см. «Хранение токена»).
3. PAT со сроком жизни.
4. Токен — `mcp-jira token` (режимы `keychain` и `file`) или переменная `MCP_JIRA_TOKEN` (режим `env`).
5. `mcp-jira serve` в `.mcp.json`.

Конфиг с прежним ключом `token_path` не загружается; переход — [SETUP.md, «Переход с env или token_path на keychain»](SETUP.md#переход-с-env-или-token_path-на-keychain).

### Запуск

Обычно сервер запускает Claude Code по записи в `.mcp.json` (SETUP.md, шаг 6). Вручную — для проверки:

```
mcp-jira [--config path] serve
mcp-jira [--config path] token
mcp-jira --help
```

Флаг `--config` пишется до подкоманды. Любая ошибка печатается в stderr с префиксом `mcp-jira: `, код выхода 1; `--help` — код 0.

- `serve` — MCP-сервер на stdio. Не стартует, если конфиг невалиден или токен не получен (см. «Хранение токена»). Guard включён всегда, флага отключения нет. Завершается по концу stdin или по первому `SIGINT`/`SIGTERM` (код выхода 0). Если штатное завершение зависло, второй сигнал убивает процесс сразу.
- `token` — записывает PAT в хранилище режима `secrets`:
  - в режимах `file` и `keychain` спрашивает токен (`Jira personal access token: `); в `keychain` при необходимости создаёт связку;
  - ввод: на macOS из терминала — без эха; через канал (`… | mcp-jira token`) — на любой ОС; на другой ОС из терминала — ошибка `token: hiding terminal input is supported only on macOS, pipe the token into stdin instead`;
  - Ctrl+C отменяет ввод: токен не сохраняется, эхо терминала восстанавливается, код выхода 1;
  - в режиме `env` — ошибка `token: secrets mode is env, set MCP_JIRA_TOKEN in the environment instead`.

## Конфиг

Путь к конфигу задаёт `--config` (по умолчанию `~/.config/mcp-jira/config.yaml`). Домашний каталог раскрывается так:

- в `--config` и `attachments.dirs` — `~` и `~/…`;
- в `secrets_dir` и `keychain_path` — только `~/…`.

Если домашний каталог не определён — `ErrHomeUnknown`.

```yaml
base_url: https://jira.example.com
secrets: keychain   # по умолчанию env
keychain_path: ~/Library/Keychains/scoped-mcp.keychain-db   # можно не указывать: это значение по умолчанию

tools:
  create_issue: true
  attach_files: true
  add_comment: true
  link_issues: true
  update_issue: true

fields:
  description: {jira: description,       kind: text}
  priority:    {jira: priority,          kind: name, required: true}
  labels:      {jira: labels,            kind: labels}
  components:  {jira: components,        kind: names}
  epic_name:   {jira: customfield_10203, kind: text, required: true}
  epic_link:   {jira: customfield_10201, kind: issue_key}

projects:
  - key: PROJ
    assign_to_self: true
    issue_types:
      - name: Epic
        id: "10000"
        fields: [description, priority, labels, components, epic_name]
      - name: История
        id: "11203"
        fields: [description, priority, labels, components, epic_link]
    update_fields: [epic_link]

attachments:
  dirs: [~/agent-out]
  extensions: [.md, .drawio, .png, .svg, .txt, .json, .log]
  max_bytes: 10485760

issue_links:
  - {relation: blocks,        type: Blocks,  direction: outward}
  - {relation: is blocked by, type: Blocks,  direction: inward}
  - {relation: relates to,    type: Relates, direction: outward}
```

- `secrets` — `env` (по умолчанию), `file` или `keychain` (только macOS), иначе `ErrSecretsMode`; `secrets_dir` и `keychain_path` — см. «Хранение токена».
- `tools` — какие инструменты включены (см. «Инструмент»): `create_issue`, `attach_files`, `add_comment`, `link_issues` и `update_issue`, по умолчанию все `false`. Должен быть включён хотя бы один. Выключенного инструмента нет в списке, и guard не пропускает его запросы.
- `fields` — общий словарь полей для `create_issue` и `update_issue`: из него разрешаются поля типам задач (`issue_types[].fields`) и полям изменения (`update_fields`). Ключ — имя параметра инструмента `create_issue` и `update_issue` (см. «Инструмент»), `jira` — ID поля в Jira, `kind` — как значение превращается в JSON, `required` — поле обязательно у каждого типа, которому оно разрешено.
- `projects` — разрешённые проекты для всех инструментов; у каждого — разрешённые типы: имя (его передают в параметре `issue_type`), ID типа в Jira (строка в кавычках) и список полей из `fields`. `issue_types` и `assign_to_self` нужны только `create_issue`, `update_fields` — только `update_issue`: без них (при `attach_files`, `add_comment` и `link_issues`) проект задаётся одним `key`, например `projects: [{key: PROJ}]`. У `add_comment` других настроек нет.
- `update_fields` у проекта — ключи из `fields`, которые `update_issue` может менять у задач этого проекта; `required` при изменении не учитывается. Пример — `epic_link: {jira: customfield_10201, kind: issue_key}` в `fields` и `update_fields: [epic_link]` у PROJ: перенос задачи между эпиками и удаление из эпика. Проект без `update_fields` задаётся как обычно, но изменить у его задач ничего нельзя. При `update_issue` без `create_issue` проекту хватает `key` и `update_fields`, например `projects: [{key: PROJ, update_fields: [epic_link]}]`.
- `assign_to_self` (по умолчанию `false`) — назначать задачи проекта на владельца токена. Перед первым созданием такой задачи сервер узнаёт логин владельца запросом `GET /rest/api/2/myself`, запоминает его до конца работы процесса и передаёт в Jira `"assignee": {"name": "<логин>"}`. Без этого ключа сервер `assignee` не передаёт, и Jira ставит исполнителя по умолчанию для проекта. Указать другого исполнителя нельзя: параметра `assignee` у инструмента нет.
- `summary` в `fields` не описывается: это базовый параметр инструмента, обязательный всегда.
- `attachments` — правила вложений для `attach_files`; раздел обязателен при `tools.attach_files: true` и запрещён без него. `dirs` — каталоги, из которых можно прикладывать файлы (вместе с подкаталогами); `extensions` — разрешённые расширения, сравниваются без учёта регистра по последнему суффиксу имени (`scheme.drawio.png` — это `.png`); `max_bytes` — предельный размер одного файла в байтах.
- `issue_links` — разрешённые отношения для `link_issues`; раздел обязателен и непуст при `tools.link_issues: true` и запрещён без него. `relation` — фраза, которую передаёт модель (параметр `relation`); `type` — имя типа связи в Jira; `direction: outward` — фраза совпадает с outward-описанием типа (у `Blocks` — «blocks»: `issue_key` блокирует `target_key`), `direction: inward` — с inward-описанием («is blocked by»: `issue_key` заблокирована `target_key`). У симметричного `Relates` направление ни на что не влияет, пишите `outward`. Описания типов в `jira.example.com`: у `Blocks` outward — «blocks», inward — «is blocked by»; у `Relates` обе стороны — «relates to». Задачи связи — из тех же `projects`, отдельного списка проектов нет.

Допустимые `kind` и во что превращается значение:

| `kind` | Значение от модели | JSON для Jira |
|---|---|---|
| `text` | строка | `"..."` |
| `name` | строка | `{"name": "..."}` |
| `names` | массив строк | `[{"name": "..."}]` |
| `option` | строка | `{"value": "..."}` |
| `options` | массив строк | `[{"value": "..."}]` |
| `labels` | массив строк | `["..."]` |
| `issue_key` | строка | `"PROJ-123"`; проект ключа должен быть из `projects` |

Валидация при старте (ошибки — `config.Err…`):

- неизвестные ключи YAML — ошибка (`malformed YAML`), в том числе в `tools` и прежний `token_path`;
- в `tools` включён хотя бы один инструмент; раздела `tools` нет — тоже ошибка (`ErrNoTools`, `tools must enable at least one tool`);
- `tools.attach_files: true` и раздел `attachments` — только вместе (`ErrAttachmentsSection`, `tools.attach_files and the attachments section must be set together`);
- `tools.link_issues: true` и непустой раздел `issue_links` — только вместе (`ErrIssueLinksSection`, `tools.link_issues and a non-empty issue_links section must be set together`);
- `tools.update_issue: true` и непустой `update_fields` хотя бы у одного проекта — только вместе (`ErrUpdateIssueFields`, `tools.update_issue and a non-empty update_fields in at least one project must be set together`);
- без `tools.create_issue: true` и без `tools.update_issue: true` `fields` пуст (`ErrFieldsSettings`, `fields are used only by tools.create_issue and tools.update_issue`);
- без `tools.create_issue: true` у проектов нет `issue_types` и `assign_to_self: true` (`ErrCreateIssueSettings`, `issue_types and assign_to_self are used only by tools.create_issue`);
- `base_url` — абсолютный `https`-URL без userinfo (`user@`, `user:password@`), query и fragment; завершающий `/` отбрасывается;
- `secrets` — `env`, `file` или `keychain`, `keychain` — только на macOS (`ErrSecretsMode`);
- `secrets_dir` и `keychain_path` — абсолютный путь или путь, начинающийся с `~/` (`ErrSecretsPath`); относительный и пустой путь, голый `~` и `~user/…` отклоняются, даже если хранилище не используется выбранным режимом;
- `projects` не пуст, ключи уникальны и соответствуют `^[A-Z][A-Z0-9_]+$`;
- при `tools.create_issue: true` у проекта есть хотя бы один тип (`ErrNoIssueTypes`); у типа непустые имя и ID; имена и ID типов в проекте уникальны;
- каждое поле типа и каждый ключ `update_fields` описаны в `fields` (`ErrUnknownField`, `issue type or update_fields refers to a field missing from fields`) и не повторяются в пределах типа или `update_fields` (`ErrDuplicateField`, `duplicate field within an issue type or update_fields`);
- `kind` из таблицы; два ключа `fields` не ссылаются на одно поле Jira;
- `jira` не пуст и не равен служебным полям Jira `project`, `issuetype`, `summary`, `issuelinks`, `assignee`: `project`, `issuetype` и `summary` сервер заполняет сам, `assignee` — только по `assign_to_self`, `issuelinks` меняет другие задачи;
- ключ поля не равен ни одному из `project`, `issue_type`, `summary` (базовые параметры `create_issue`), `issue_key` (параметр `update_issue` с ключом изменяемой задачи: поле с таким ключом перекрыло бы его в схеме) и `assignee` (исполнителя задаёт `assign_to_self`) (`ErrFieldKey`, `field key is reserved`);
- если раздел `attachments` есть: `dirs` не пуст, каждый каталог — абсолютный путь или путь от `~/` и остаётся абсолютным после раскрытия `~` (`ErrAttachmentsDirs`); каталог не должен давать доступ к домашнему каталогу — иначе вложениями станут доступны, например, `.json` и `.txt` из `~/.config` (`ErrAttachmentsDirs`). При загрузке конфига отклоняются очевидные случаи: каталог после раскрытия симлинков — корень или домашний (по идентичности, `os.SameFile`, поэтому и в другом регистре на нечувствительной к регистру ФС) или по пути лежит выше реального домашнего, например `~/..` или `/Users` на macOS, `/home` в Linux. Главная проверка — в инструменте, для каждого файла (шаг 3.4 в разделе инструмента `attach_files`): она ловит и каталоги, внутри которых домашний виден под другим путём (`/System`, `/System/Volumes`, `/System/Volumes/Data` на APFS, bind-mount в Linux). Домашний каталог — и `HOME`, и домашний каталог учётной записи (каждый, если путь абсолютный и существует); несуществующий каталог из `dirs` при загрузке конфига не проверяется; `extensions` не пуст, каждое расширение начинается с `.`, длиннее одного символа, без `/` и не повторяется без учёта регистра (`ErrAttachmentsExtensions`); `max_bytes` больше нуля и не больше 20 MiB, `20971520` (`ErrAttachmentsMaxBytes`). Потолок связан с таймаутом клиента 30 с и с памятью: файл целиком держится в памяти несколько раз (содержимое, тело multipart у клиента, копия и пересборка у guard).
- при `tools.link_issues: true`: у каждой строки `issue_links` `relation` непуст, без пробелов по краям и не повторяется (`ErrIssueLinkRelation`, `issue_links relation must be non-empty, without surrounding spaces and unique`); `type` непуст (`ErrIssueLinkType`, `issue_links type must be non-empty`); `direction` — `outward` или `inward` (`ErrIssueLinkDirection`, `issue_links direction must be outward or inward`).

### Хранение токена

Через общую библиотеку `pkg/secret` монорепозитория scoped-mcp, имя секрета `token`. Токен читается только из хранилища режима `secrets`, другие хранилища не проверяются. Пробельные символы по краям обрезаются.

| `secrets` | Где лежит токен | Ключ конфига с путём (значение по умолчанию) | Как записать |
|---|---|---|---|
| `env` (по умолчанию) | переменная `MCP_JIRA_TOKEN` | — | в окружении Claude Code |
| `file` | файл `token` в `secrets_dir`: каталог `0700`, файл `0600`; доступный группе или остальным не читается | `secrets_dir` (`~/.config/mcp-jira/secrets`) | `mcp-jira token` |
| `keychain` (macOS) | элемент с service `mcp-jira` и account `token` в отдельной связке `keychain_path` | `keychain_path` (`~/Library/Keychains/scoped-mcp.keychain-db`) | `mcp-jira token` |

Связка `scoped-mcp.keychain-db` общая с другими MCP (`mcp-tg`, `mcp-gitlab`, `mcp-confluence`): один пароль, разные элементы. Если её нет, `mcp-jira token` создаёт её и печатает `Creating keychain <путь>: choose its password in the macOS dialog`; пароль задаётся в системном диалоге и через процесс `mcp-jira` не проходит.

macOS даёт доступ к элементу без диалога только бинарю, который его создал (по хешу подписи), и бинарям, разрешённым кнопкой «Разрешать всегда». Поэтому `mcp-jira token` запускается уже установленным бинарём, а `make install-mcp-jira` (из корня scoped-mcp):

- собирает только закоммиченное состояние всего репозитория scoped-mcp и останавливается на нечистом дереве;
- сверяет загруженные модули с `go.sum` (`go mod verify`);
- ставит с `GOFLAGS=-mod=readonly` в `$(go env GOPATH)/bin`;
- подписывает бинарь `codesign -s - -f -o runtime`: hardened runtime закрывает процесс от отладчика и `DYLD_INSERT_LIBRARIES`, а ACL элемента связки доверяет хешу именно подписанного бинаря.

После каждой пересборки первое обращение к элементу показывает диалог доступа к элементу связки — это ожидаемо сразу после своего `make install-mcp-jira` и подозрительно в любой другой момент (см. [SETUP.md](SETUP.md), «Обновление mcp-jira»).

Ошибки хранения токена — переменные `config.Err…` (проверяются через `errors.Is`); в stderr печатаются с префиксом `mcp-jira: `:

| Ошибка | Текст |
|---|---|
| `ErrSecretsMode` | `config: secrets must be env, file or keychain: …` |
| `ErrSecretsPath` | `config: secrets_dir and keychain_path must be absolute or start with ~/: "<путь>"` |
| `ErrHomeUnknown` | `config: home directory is unknown, ~ cannot be expanded: …` |
| `ErrTokenUnavailable` | `config: token is not available: <причина из хранилища>` или `config: token is not available: token is empty` |

## Инструмент

Инструменты включаются флагами раздела `tools`: `create_issue` — при `tools.create_issue: true`, `attach_files` — при `tools.attach_files: true`, `add_comment` — при `tools.add_comment: true`, `link_issues` — при `tools.link_issues: true`, `update_issue` — при `tools.update_issue: true`.

`create_issue`:

| Параметр | Тип | Обязательный |
|---|---|---|
| `project` | строка, enum ключей `projects` | да |
| `issue_type` | строка, enum имён типов всех проектов | да |
| `summary` | строка, не короче 1 символа | да |
| поля из `fields` | по `kind`: строка или массив строк | по `required` для выбранного типа |

Описание инструмента собирается из конфига: для каждого проекта — его типы, для каждого типа — поля с их `kind`, обязательные помечены `*`; у проектов с `assign_to_self` — пометка `(issues are assigned to the token owner, i.e. the user)`; в конце — форма значения для каждого `kind`. Описание нужно потому, что схема не может выразить, какие типы есть у какого проекта и какие поля у какого типа.

Ответ при успехе: `{"key": "PROJ-7790", "url": "https://jira.example.com/browse/PROJ-7790"}`.

Аргументы проверяются до запроса в два этапа:

1. MCP SDK сверяет их со схемой: enum `project` и `issue_type`, типы значений, непустой `summary`, отсутствие параметров вне схемы. Ошибка начинается с `validating "arguments"`.
2. Инструмент сверяет их с конфигом (повторно, независимо от схемы) в таком порядке; ошибки — `tools.Err…`:
   1. `project` передан и является строкой (`ErrFieldRequired`, `ErrFieldValue`);
   2. проект есть в конфиге (`ErrProjectNotAllowed`, `project is not allowed by the config`);
   3. `issue_type` передан и является строкой (`ErrFieldRequired`, `ErrFieldValue`);
   4. тип разрешён в этом проекте (`ErrIssueTypeNotAllowed`, `issue type is not allowed for this project`). Схема перечисляет типы всех проектов одним списком, поэтому тип другого проекта проходит этап 1 и отсекается только здесь;
   5. `summary` передан, является строкой и не пуст после обрезки пробелов (`ErrFieldRequired`, `required field is empty`; не строка — `ErrFieldValue`);
   6. каждый остальной параметр разрешён типу, параметры перебираются по алфавиту (`ErrFieldNotAllowed`, `field is not allowed by the config: <поле> for issue type <тип>`);
   7. значение каждого переданного поля, в порядке `fields` типа, имеет форму своего `kind` (`ErrFieldValue`, `field value has wrong shape for its kind`); `issue_key` выглядит как `PROJ-123` и принадлежит проекту из конфига (`ErrIssueKeyInvalid`);
   8. обязательные поля типа не пусты (`ErrFieldRequired`); список из одних пустых строк считается пустым.

Все ошибки возвращаются как результат `tools/call` с `isError: true`, не как ошибка протокола. Существование значений (приоритет, компонент) сервер не проверяет — это делает Jira при создании.

`attach_files` — только при `tools.attach_files: true` (правила — из раздела `attachments`):

| Параметр | Тип | Обязательный |
|---|---|---|
| `issue_key` | строка, ключ задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` | да |
| `paths` | массив непустых строк, хотя бы один элемент | да |

Задача — любая задача проектов из `projects`, в том числе созданная раньше и чужая. Числовые ID задач не принимаются. Описание инструмента собирается из конфига: разрешённые проекты, каталоги, расширения и предельный размер файла.

Ответ при успехе: `{"issue_key": "PROJ-7790", "url": "https://jira.example.com/browse/PROJ-7790", "attached": [{"file": "report.md", "id": "10501"}]}`, где `id` — ID вложения из ответа Jira.

До любого запроса в Jira проверяется ключ задачи и весь список путей: если хоть один путь не проходит, не загружается ничего. Порядок проверок (ошибки — `tools.Err…`):

1. `issue_key` выглядит как `PROJ-123`, и `PROJ` есть в `projects` (`ErrIssueKeyInvalid`).
2. Каталоги `dirs` берутся после раскрытия симлинков; несуществующие пропускаются.
3. `paths` не пуст. Для каждого пути по порядку:
   1. путь непустой, абсолютный или от `~/`, `~` раскрывается (`ErrAttachmentPath`);
   2. симлинки раскрываются (`filepath.EvalSymlinks`); файла нет — `ErrAttachmentPath`; имя реального файла содержит `\` или управляющий символ (код меньше `0x20` или `0x7f`, например перевод строки) — `ErrAttachmentPath` (guard не пропускает `\` в имени вложения, а отказ после части загрузок оставил бы задачу с половиной файлов; управляющие символы отклоняются там же, до загрузок);
   3. реальный путь лежит внутри одного из каталогов `dirs`, тоже взятых после раскрытия симлинков; сравнение по компонентам пути: `/a/out2` не внутри `/a/out` (`ErrAttachmentOutsideDirs`);
   4. путь от каталога из `dirs` до файла не проходит через домашний каталог или корень: каждый каталог от родителя файла вверх до каталога из `dirs` включительно сравнивается с ними по идентичности (`os.SameFile`), каталог, для которого `stat` не удался, считается запрещённым; файл проходит, если так можно дойти хотя бы от одного каталога из `dirs`, в котором он лежит, иначе — `ErrAttachmentThroughHome`. Так файл `~/agent-out/x.md` из `dirs: [~/agent-out]` проходит (проверяется только `out`), а `~/.config/x.json` через `dirs: [/System/Volumes]` (виден как `/System/Volumes/Data/Users/<user>/.config/x.json`) или через симлинк в `dirs`, который заменили на `~`, — нет. Домашний — `HOME` и домашний учётной записи; корень отклоняется и при неизвестном `HOME`;
   5. файл не является файлом токена режима `file` (`secrets_dir/token`), файлом связки (`keychain_path`) и файлом конфига — при любом режиме `secrets`; сравниваются сами файлы, а не пути, поэтому симлинк и жёсткая ссылка на них тоже отклоняются (`ErrAttachmentForbiddenFile`);
   6. обычный файл, не каталог, не канал, не устройство и не сокет (`ErrAttachmentNotRegularFile`);
   7. расширение реального файла есть в `extensions` (`ErrAttachmentExtension`);
   8. размер не больше `max_bytes` (`ErrAttachmentTooLarge`);
   9. файл открывается на чтение — например, права `000` (`ErrAttachmentPath` с причиной);
   10. тот же реальный файл не указан дважды (`ErrAttachmentPath`).
4. Файлы загружаются по одному на запрос, последовательно, в порядке `paths`. Имя вложения — имя реального файла (для симлинка — имя файла, на который он указывает). При загрузке файл открывается заново (первое открытие — проверка читаемости в шаге 3), без блокировки (`O_NONBLOCK`: подложенный именованный канал не повесит инструмент); всё дальнейшее — по этому дескриптору, и при любом отказе файл не отправляется:
   1. файл открывается: исчез после проверки — `ErrAttachmentChanged`, другая ошибка открытия (например, права сняли) — `ErrAttachmentPath` с причиной;
   2. это тот же файл, что при проверке (`os.SameFile`), иначе — `ErrAttachmentChanged`;
   3. размер не больше `max_bytes`, иначе — `ErrAttachmentTooLarge`;
   4. содержимое читается из того же дескриптора с пределом `max_bytes` + 1 байт; файл вырос во время чтения — `ErrAttachmentTooLarge`.

Если файл не загрузился, остальные не загружаются, а ошибка (`tools.AttachFilesError`) перечисляет уже приложенные файлы с их ID, называет файл, на котором загрузка остановилась, и файлы после него, которые не пробовались, например: `tools: attached to PROJ-7790: report.md (id 10501); scheme.png was not attached: <причина>; not attempted after it: notes.txt. Retrying would attach report.md again: Jira does not deduplicate attachments, so retry only the files that were not attached`. Если файл последний, части `not attempted after it` нет; если до него ничего не приложено, текст начинается с `tools: no files were attached to PROJ-7790 before scheme.png`, а фразы про повтор нет.

Если Jira не подтвердила загрузку (`jira.ErrAttachUnconfirmed`), файл мог быть приложен, и текст другой: `…; scheme.png may have been attached, check the issue before retrying it: <причина>; not attempted after it: notes.txt. Retrying would attach report.md again: Jira does not deduplicate attachments, so retry only the files after scheme.png, and scheme.png only if it is missing from the issue`; если он последний — `… so retry scheme.png only if it is missing from the issue`. Jira не отсеивает повторы: повторная загрузка уже приложенного файла создаёт второе вложение с тем же именем.

`add_comment` — только при `tools.add_comment: true`:

| Параметр | Тип | Обязательный |
|---|---|---|
| `issue_key` | строка, ключ задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` | да |
| `body` | строка, не короче 1 символа | да |

Задача — любая задача проектов из `projects`, в том числе чужая. Числовые ID задач не принимаются. `body` — текст в Jira wiki markup (`h3. Заголовок`, `*жирный*`, `{code}…{code}`, `[текст|url]`), уходит в Jira как есть, без обрезки пробелов. Видимость (`visibility`) и свойства комментария не задаются: комментарий виден всем, кто видит задачу. Описание инструмента собирается из конфига: разрешённые проекты, формат текста и предупреждение о повторе.

Ответ при успехе: `{"issue_key": "PROJ-7790", "id": "10100", "url": "https://jira.example.com/browse/PROJ-7790?focusedCommentId=10100#comment-10100"}`, где `id` — ID комментария из ответа Jira, `url` — ссылка на задачу с прокруткой к комментарию.

Аргументы проверяются до запроса: сначала MCP SDK по схеме (шаблон `issue_key`, непустой `body`, отсутствие параметров вне схемы), затем инструмент (ошибки — `tools.Err…`):

1. `issue_key` выглядит как `PROJ-123`, и `PROJ` есть в `projects` (`ErrIssueKeyInvalid`);
2. `body` не пуст после обрезки пробелов (`ErrFieldRequired`, `required field is empty: body`).

Повтор. Если Jira не подтвердила комментарий (`jira.ErrCommentUnconfirmed`, `the comment was sent but Jira did not confirm it; the comment may have been added, check the issue before retrying`), он мог появиться: Jira не отсеивает повторы, и повторный вызов добавит второй такой же комментарий. Сначала проверить задачу. Остальные ошибки означают, что комментарий не добавлен: `401` — выпустить новый токен, `StatusError` с `404` — задачи нет или нет прав на неё, `400` — Jira отклонила текст, отказ guard — запрос не отправлялся.

`link_issues` — только при `tools.link_issues: true` (отношения — из раздела `issue_links`):

| Параметр | Тип | Обязательный |
|---|---|---|
| `issue_key` | строка, ключ задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` | да |
| `relation` | строка, enum фраз `issue_links[].relation` | да |
| `target_key` | строка, ключ задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` | да |

Связь читается как «`issue_key` `relation` `target_key`»: `issue_key=PROJ-123`, `relation=blocks`, `target_key=PROJ-124` — это `PROJ-123 blocks PROJ-124`, то есть PROJ-123 блокирует PROJ-124. Тип связи и сторону сервер берёт из строки `issue_links` с этой фразой.

Обе задачи — любые задачи проектов из `projects`, в том числе чужие. Числовые ID задач не принимаются. Описание инструмента собирается из конфига: разрешённые проекты, пример чтения связи, список фраз и предупреждение о повторе.

Ответ при успехе: `{"issue_key": "PROJ-123", "relation": "blocks", "target_key": "PROJ-124", "url": "https://jira.example.com/browse/PROJ-123"}`, где `url` — ссылка на задачу `issue_key`. Тело ответа `201` сервер не читает, поэтому ID связи не возвращает.

Аргументы проверяются до запроса: сначала MCP SDK по схеме (шаблон ключей, enum `relation`, отсутствие параметров вне схемы), затем инструмент (ошибки — `tools.Err…`):

1. `issue_key` выглядит как `PROJ-123`, и `PROJ` есть в `projects` (`ErrIssueKeyInvalid`, `issue key must look like PROJ-123 and belong to an allowed project`, с уточнением `issue_key=…`);
2. то же для `target_key` (`ErrIssueKeyInvalid`, `issue key must look like PROJ-123 and belong to an allowed project`, с уточнением `target_key=…`);
3. `relation` есть среди `issue_links[].relation` — точно, с учётом регистра и без обрезки пробелов (`ErrRelationNotAllowed`, `relation is not allowed by the config`);
4. `issue_key` и `target_key` различны (`ErrLinkToItself`, `issue_key and target_key must be different issues`).

Повтор. Если Jira не подтвердила связь (`jira.ErrLinkUnconfirmed`, `the link was sent but Jira did not confirm it; the link may have been created, check the issue before retrying`), связь могла быть создана: перед повтором проверить задачу. Остальные ошибки означают, что связь не создана: `401` — токен истёк или отозван (выпустить новый) или у владельца токена нет права Link Issues в проекте, `StatusError` с `404` — связи в Jira выключены, одной из задач нет или нет прав на неё, или в Jira нет такого типа связи (сверить `type` с `jira_get_link_types` в MCP `jira-readonly`), `400` — Jira отклонила связь, отказ guard — запрос не отправлялся.

`update_issue` — только при `tools.update_issue: true` (поля — из `update_fields` проекта задачи):

| Параметр | Тип | Обязательный |
|---|---|---|
| `issue_key` | строка, ключ задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` | да |
| поля из `update_fields` всех проектов | по `kind`: строка или массив строк, либо `null` | нет, но хотя бы одно |

Меняет только переданные поля задачи; остальные поля Jira не трогает. С полем `epic_link` (`kind: issue_key`, Epic Link): ключ эпика — перенести задачу в этот эпик (из другого эпика или без эпика), `null` — убрать задачу из эпика. `null` очищает любое поле из `update_fields`; любое другое значение уходит в Jira по форме своего `kind` (таблица в «Конфиг»), без проверки на пустоту; значение `kind: issue_key` по-прежнему должно быть ключом задачи из `projects`.

Задача — любая задача проектов с непустым `update_fields`, в том числе чужая. Числовые ID задач не принимаются. Схема объединяет `update_fields` всех проектов, поэтому какие поля можно менять у задач какого проекта, видно только из описания инструмента: оно собирается из конфига — проекты с непустым `update_fields` и их поля с `kind`, правило ключа задачи, требование передать хотя бы одно поле, очистка через `null`, форма значения для каждого `kind` и пометка, что повтор безопасен.

Ответ при успехе: `{"key": "PROJ-7790", "url": "https://jira.example.com/browse/PROJ-7790"}`.

Аргументы проверяются до запроса: сначала MCP SDK по схеме (шаблон `issue_key`, типы значений с `null`, отсутствие параметров вне схемы — поле, которого нет ни в одном `update_fields`, даёт `unexpected additional properties`), затем инструмент (ошибки — `tools.Err…`):

1. `issue_key` передан и является строкой (`ErrFieldRequired`, `ErrFieldValue`);
2. `issue_key` выглядит как `PROJ-123`, и `PROJ` есть в `projects` (`ErrIssueKeyInvalid`, с уточнением `issue_key=…`);
3. каждый остальной параметр есть в `update_fields` проекта задачи, параметры перебираются по алфавиту (`ErrFieldNotAllowed`, `field is not allowed by the config: <поле>: not in update_fields of project <проект>`). Схема общая для всех проектов, поэтому поле из `update_fields` другого проекта проходит её и отсекается только здесь;
4. значение каждого переданного поля, кроме `null`, в порядке `update_fields` имеет форму своего `kind` (`ErrFieldValue`); значение поля `kind: issue_key` выглядит как `PROJ-123` и принадлежит проекту из конфига (`ErrIssueKeyInvalid`, с уточнением `<поле>=…`);
5. передано хотя бы одно поле, кроме `issue_key` (`ErrNoFieldsToUpdate`, `update_issue needs at least one field besides issue_key: <ключ задачи>`).

Повтор. Изменение идемпотентно: повторный вызов с теми же аргументами записывает в поля те же значения. Если Jira не подтвердила изменение (`jira.ErrUpdateUnconfirmed`, `the update was sent but Jira did not confirm it; the update is idempotent, it is safe to retry`), его можно просто повторить. Остальные ошибки означают, что изменение не применено: `401` — выпустить новый токен, `StatusError` с `404` — задачи нет или нет прав на неё, `400` — Jira отклонила значение или поле (сообщение Jira — после статуса), отказ guard — запрос не отправлялся.

## Гарантии

- Guard пропускает в сеть запрос на создание, только если выполнены все условия:
  - `tools.create_issue: true`;
  - метод `POST`;
  - схема и хост URL совпадают с `base_url`, заголовок `Host`, если задан, — тоже;
  - путь — `<путь base_url>/rest/api/2/issue` без процентного кодирования, query пуст;
  - тело не больше 1 MiB и является JSON-объектом с единственным ключом `fields`, без данных после него;
  - в теле, `fields`, `project` и `issuetype` нет повторяющихся ключей;
  - `fields.project` — объект с единственным ключом `key` из `projects`;
  - `fields.issuetype` — объект с единственным ключом `id` из типов этого проекта;
  - остальные ключи `fields` — `summary` и ID полей Jira, разрешённых этому типу;
  - `fields.assignee` — только у проекта с `assign_to_self` и только объект с единственным ключом `name`, равным логину, который guard сам запомнил из ответа `myself` (см. ниже). Пока логин не запомнен, `assignee` не пропускается.
- Загрузку файла guard пропускает, только если `tools.attach_files: true`, в конфиге есть раздел `attachments` и выполнены все условия:
  - метод `POST`; схема, хост, `Host`, кодирование пути и query — как у создания;
  - путь — `<путь base_url>/rest/api/2/issue/<KEY>/attachments`, где `<KEY>` соответствует `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`, а проект ключа есть в `projects`;
  - заголовок `X-Atlassian-Token` ровно один, со значением `no-check`;
  - заголовок `Content-Type` ровно один: `multipart/form-data` с единственным параметром `boundary`;
  - тело не больше `max_bytes` + 64 KiB на оболочку multipart;
  - в теле ровно одна часть: `Content-Disposition: form-data; name="file"; filename="<имя>"`, `Content-Type: application/octet-stream`; имя непустое, без `/` и `\`, с расширением из `extensions` (без учёта регистра); содержимое не больше `max_bytes`;
  - тело побайтно совпадает с тем, что guard собирает сам из разобранного имени и содержимого (`mime/multipart` с тем же `boundary`): никаких других частей, заголовков части, параметров вроде `filename*`, `Content-Transfer-Encoding`, данных до первой границы и после закрывающей.

  Guard проверяет ключ задачи, а не её фактический проект: задача, перенесённая из разрешённого проекта в другой, доступна по старому ключу, и Jira перенаправит загрузку в неё. Каталоги, запрет файла токена и конфига проверяет инструмент: guard о файловой системе не знает.
- Комментарий guard пропускает, только если `tools.add_comment: true` и выполнены все условия:
  - метод `POST`; схема, хост, `Host`, кодирование пути и query — как у создания;
  - путь — `<путь base_url>/rest/api/2/issue/<KEY>/comment`, где `<KEY>` соответствует `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`, а проект ключа есть в `projects`; путь конкретного комментария (`…/comment/<id>`) не пропускается;
  - тело не больше 1 MiB и является JSON-объектом с единственным ключом `body`, без повторяющихся ключей и данных после него;
  - `body` — строка, непустая после обрезки пробелов.

  Поэтому `visibility`, `properties` и другие ключи комментария в запрос не попадают, а чтение, правка и удаление комментариев (`GET`, `PUT`, `DELETE`) отклоняются. Как и у вложений, guard проверяет ключ задачи, а не её фактический проект.
- Связь guard пропускает, только если `tools.link_issues: true` и выполнены все условия:
  - метод `POST`; схема, хост, `Host`, кодирование пути и query — как у создания;
  - путь — `<путь base_url>/rest/api/2/issueLink`; путь конкретной связи (`…/issueLink/<id>`) и `…/issueLinkType` не пропускаются;
  - тело не больше 1 MiB и является JSON-объектом ровно с ключами `type`, `inwardIssue` и `outwardIssue`, без повторяющихся ключей и данных после него;
  - `type` — объект с единственным ключом `name`, равным одному из `issue_links[].type`;
  - `inwardIssue` и `outwardIssue` — объекты с единственным полем `key` — ключом задачи по шаблону `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`, проект которого есть в `projects`;
  - ключи задач в `inwardIssue.key` и `outwardIssue.key` различны.

  Поэтому поле `comment` тела `issueLink` (комментарий при создании связи) в запрос не попадает, а чтение и удаление связей (`GET`/`DELETE …/issueLink/<id>`) отклоняются. Как и у вложений, guard проверяет ключ задачи, а не её фактический проект.

  Какая задача уходит в `inwardIssue`, решает клиент: при `direction: outward` — `issue_key`, а `target_key` — в `outwardIssue`, при `inward` — наоборот. Это рабочая гипотеза, она проверяется вживую (SETUP.md, шаг 7, пункт 5); guard направление не сверяет.
- Изменение задачи guard пропускает, только если `tools.update_issue: true` и выполнены все условия:
  - метод `PUT`; схема, хост, `Host`, кодирование пути и query — как у создания;
  - путь — `<путь base_url>/rest/api/2/issue/<KEY>` без подресурса после ключа, где `<KEY>` соответствует `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`, а проект ключа есть в `projects`;
  - тело не больше 1 MiB и является JSON-объектом с единственным ключом `fields`, без повторяющихся ключей (в теле и в `fields`) и данных после него;
  - `fields` — непустой объект, каждый ключ которого — ID поля Jira из `update_fields` проекта задачи.

  Значения полей guard не проверяет: их форму и `null` проверяет инструмент. Поэтому `update`, `transition`, `properties` и поля вне `update_fields` в запрос не попадают, а `DELETE` задачи и `PUT` на её подресурсы отклоняются. Как и у вложений, guard проверяет ключ задачи, а не её фактический проект.
- Кроме создания, загрузки файлов, комментариев, связей и изменения задачи, guard пропускает только запрос владельца токена, и только если `tools.create_issue: true` и хотя бы у одного проекта `assign_to_self: true`:
  - метод `GET`, путь — `<путь base_url>/rest/api/2/myself`; схема, хост, `Host`, кодирование пути и query — как у создания;
  - тела нет.

  На ответ `200` guard читает тело (не больше 1 MiB), запоминает `name` и отдаёт клиенту ответ с тем же телом; ответ с другим статусом отдаёт как есть и ничего не запоминает. Тело больше 1 MiB или ошибка чтения — `guard.ErrTokenOwnerResponse`.

  Иначе запрос отклоняется до отправки (`guard.ErrForbidden`). Поэтому `update` и `issuelinks` в запрос не попадают, и создание задачи не меняет другие задачи. Флаги `tools` guard проверяет сам, не полагаясь на валидацию конфига: при выключенном `create_issue` создание отклоняется, даже если у проектов заданы типы.
- Отправляется ровно то тело, которое проверил guard, в том числе при повторной отправке внутри `net/http`. Исходное тело всегда закрывается.
- Клиент не ходит по редиректам (`CheckRedirect` → `http.ErrUseLastResponse`), таймаут 30 с на запрос, в том числе на загрузку файла, повторов нет: ни создание, ни загрузка, ни комментарий, ни связь не идемпотентны; изменение задачи идемпотентно, но и его клиент сам не повторяет.
- Разбор ответа Jira (ошибки — `jira.Err…`, все тексты начинаются с `jira: `):

  | Ответ | Результат |
  |---|---|
  | `201` с ключом задачи | `key` и `url` |
  | `201`, ключ не прочитан | `ErrUnexpectedResponse`: `the issue was created (201) but its key could not be read from the response; find it in Jira, do not retry` |
  | другой `2xx` | `ErrMaybeCreated`: `the request was sent but Jira did not confirm the creation; the issue may have been created, check Jira before retrying` |
  | `401` | `ErrUnauthorized`: `401 Unauthorized: the token has expired or was revoked; issue a new personal access token and save it with mcp-jira token or set MCP_JIRA_TOKEN` |
  | `5xx` | `ErrMaybeCreated` вместе со `StatusError`: статус и сообщения Jira сохраняются, например `jira: the request was sent … check Jira before retrying: jira: 503 Service Unavailable: …`. Jira могла создать задачу до сбоя: например, прокси ответил `504`, пока Jira ещё обрабатывала запрос |
  | `3xx` и `4xx`, кроме `401` | `StatusError`: `<код> <статус>`, например `jira: 403 Forbidden`; если Jira вернула сообщения, после `: ` идут тексты из `errorMessages` и `errors` (`поле: сообщение`) через `; ` |
  | сетевая ошибка или таймаут | `ErrMaybeCreated`. По сетевой ошибке нельзя понять, дошёл ли запрос до Jira, поэтому даже неустановленное соединение считается возможным созданием |
  | отказ guard | `guard: forbidden: …`, запрос не отправлялся |

  Для проекта с `assign_to_self` до создания идёт запрос владельца токена. Его ошибки означают, что создание не отправлялось:

  | Ответ на `GET …/myself` | Результат |
  |---|---|
  | `200` с непустым `name` | логин запоминается, дальше — создание |
  | `401` | `ErrUnauthorized` |
  | `200` без `name` или не JSON | `ErrTokenOwnerUnknown`: `could not determine the token owner to assign the issue to; the issue was not created, it is safe to retry` |
  | другой статус, кроме `401` | `ErrTokenOwnerUnknown` вместе со `StatusError` (статус и сообщения Jira) |
  | сетевая ошибка или таймаут | `ErrTokenOwnerUnknown` с причиной |
  | тело больше 1 MiB или не читается (`guard.ErrTokenOwnerResponse`) | `ErrTokenOwnerUnknown` вместе с `guard.ErrTokenOwnerResponse` |
  | отказ guard | `guard: forbidden: …` |

  Ошибка не запоминается: следующий вызов снова спрашивает владельца токена.

  Ответ на загрузку файла (`POST …/issue/<KEY>/attachments`):

  | Ответ | Результат |
  |---|---|
  | `200` с массивом вложений | `id` первого элемента |
  | `200` с пустым массивом, без `id`, не JSON или нечитаемое тело | `ErrAttachUnconfirmed`: `the file was sent but Jira did not confirm the attachment; the file may have been attached, check the issue before retrying` |
  | другой `2xx` | `ErrAttachUnconfirmed` |
  | `401` | `ErrUnauthorized` |
  | `5xx` | `ErrAttachUnconfirmed` вместе со `StatusError` |
  | `3xx` и `4xx`, кроме `401` | `StatusError`; `404` — задачи нет или нет прав на неё, `413` — файл слишком большой для Jira |
  | сетевая ошибка или таймаут | `ErrAttachUnconfirmed` с причиной |
  | отказ guard | `guard: forbidden: …`, запрос не отправлялся |

  Ответ на комментарий (`POST …/issue/<KEY>/comment`):

  | Ответ | Результат |
  |---|---|
  | `201` с непустым `id` | `id` и ссылка на комментарий |
  | `201` без `id`, не JSON или нечитаемое тело | `ErrCommentUnconfirmed`: `the comment was sent but Jira did not confirm it; the comment may have been added, check the issue before retrying` |
  | другой `2xx` | `ErrCommentUnconfirmed` |
  | `401` | `ErrUnauthorized` |
  | `5xx` | `ErrCommentUnconfirmed` вместе со `StatusError` |
  | `3xx` и `4xx`, кроме `401` | `StatusError`; `404` — задачи нет или нет прав на неё, `400` — Jira отклонила текст |
  | сетевая ошибка или таймаут | `ErrCommentUnconfirmed` с причиной |
  | отказ guard | `guard: forbidden: …`, запрос не отправлялся |

  Ответ на связь (`POST …/issueLink`):

  | Ответ | Результат |
  |---|---|
  | `201` | успех; тело не читается |
  | другой `2xx` | `ErrLinkUnconfirmed`: `the link was sent but Jira did not confirm it; the link may have been created, check the issue before retrying` |
  | `401` | `ErrUnauthorized`; для связи — также если у владельца токена нет права Link Issues в проекте |
  | `5xx` | `ErrLinkUnconfirmed` вместе со `StatusError` |
  | `3xx` и `4xx`, кроме `401` | `StatusError`; `404` — связи в Jira выключены, одной из задач нет или нет прав на неё, или нет такого типа связи; `400` — Jira отклонила связь |
  | сетевая ошибка или таймаут | `ErrLinkUnconfirmed` с причиной |
  | отказ guard | `guard: forbidden: …`, запрос не отправлялся |

  Ответ на изменение задачи (`PUT …/issue/<KEY>`):

  | Ответ | Результат |
  |---|---|
  | `204` | успех; тело не читается |
  | другой `2xx` | `ErrUpdateUnconfirmed`: `the update was sent but Jira did not confirm it; the update is idempotent, it is safe to retry` |
  | `401` | `ErrUnauthorized` |
  | `5xx` | `ErrUpdateUnconfirmed` вместе со `StatusError` |
  | `3xx` и `4xx`, кроме `401` | `StatusError`; `404` — задачи нет или нет прав на неё, `400` — Jira отклонила значение поля |
  | сетевая ошибка или таймаут | `ErrUpdateUnconfirmed` с причиной |
  | отказ guard | `guard: forbidden: …`, запрос не отправлялся |

  `ErrMaybeCreated` при загрузке, комментарии, связи и изменении не возникает никогда: она про создание задачи.

- Токен не попадает в ответы, описание инструмента и stderr; в ошибках токена печатается только причина из хранилища (имя переменной, путь к файлу или ошибка связки), без значения.
- В `serve` stdout занят протоколом MCP; в stderr попадает только ошибка, с которой `serve` завершился. Ошибки инструмента в stderr не пишутся: модель получает их ответом `tools/call`.

Правило «каталог вложений не даёт доступ к домашнему» сравнивает каталоги по идентичности (устройство и inode), поэтому не ловит копии домашнего с другими устройством и inode (снапшоты Time Machine под `/Volumes`, зеркала FUSE и bindfs) и гонку, в которой тот же пользователь между проверкой и загрузкой подменяет компонент пути симлинком; от агента с Bash это правило и не защищает — он прочитает файл сам.

Гарантии не защищают от агента, который сам прочитает токен и обратится к Jira в обход сервера — см. [SETUP.md, «Ограничение: агент может прочитать токен»](SETUP.md#ограничение-агент-может-прочитать-токен).

## Ручная проверка

Для проверки без Jira годится любой непустой токен: `tools/list` и отклонённые вызовы в сеть не ходят. Фиктивный токен передаётся переменной, поэтому проверка идёт на временной копии конфига (как выше) в режиме `env`. Из корня scoped-mcp:

```bash
make build-mcp-jira
CONFIG=$(mktemp)
sed -e '/^secrets:/d' -e '/^secrets_dir:/d' -e '/^keychain_path:/d' ~/.config/mcp-jira/config.yaml > "$CONFIG"
printf 'secrets: env\n' >> "$CONFIG"
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_issue","arguments":{"project":"SECRET","issue_type":"Epic","summary":"x"}}}' \
    '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_issue","arguments":{"project":"PROJ","issue_type":"Epic","summary":"x","priority":"Medium"}}}' \
    '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"update_issue","arguments":{"issue_key":"PROJ-1"}}}'
  sleep 3; } | MCP_JIRA_TOKEN=fake ./bin/mcp-jira --config "$CONFIG" serve
rm -f "$CONFIG"
```

`sleep` держит stdin открытым: на конце ввода MCP SDK закрывает соединение, и ответы на ещё не обработанные запросы теряются.

Ожидается:

- `"id":2` — инструменты, включённые в `tools`: при конфиге выше — `add_comment`, `attach_files`, `create_issue`, `link_issues` и `update_issue`; в описании `update_issue` — строка `- PROJ: epic_link (issue_key)`; в описании `create_issue` `Project PROJ (issues are assigned to the token owner, i.e. the user):` и строки `- Epic: …` и `- История: …`;
- `"id":3` — `isError: true`, текст `… enum: SECRET does not equal any of: [PROJ]`;
- `"id":4` — `isError: true`, текст `tools: required field is empty: epic_name`;
- `"id":5` — `isError: true`, текст `tools: update_issue needs at least one field besides issue_key: PROJ-1`.

В Jira ничего не создаётся и не меняется. Если вместо ответов сразу пришла строка в stderr — это ошибка конфига или токена (см. «Конфиг» и «Хранение токена»).

Проверка с настоящим токеном в Claude Code — [SETUP.md, шаг 7](SETUP.md#7-проверить-в-сессии).

## Разработка

Из корня scoped-mcp:

```bash
make generate            # mockgen
make lint                # golangci-lint v2 через go tool (включает gofumpt и goimports)
make test
make build-mcp-jira      # bin/mcp-jira
make install-mcp-jira    # только с чистого дерева scoped-mcp
```

`make generate`, `make lint` и `make test` проходят по всем серверам монорепозитория.
