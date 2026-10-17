# Настройка mcp-confluence: от выбора корней до первого вызова

Команды копируются блоками и выполняются по порядку в обычном терминале (zsh). Через `!` в Claude Code этого делать нельзя: ввод и вывод такой команды попадают в контекст агента, а токен туда попадать не должен. Агенту не поручается ни один шаг; браузер и системные диалоги macOS — только вручную.

Нужны Go не ниже версии из `go.mod` в корне scoped-mcp (`go 1.26.0`), Claude Code CLI (`claude`) и клон репозитория scoped-mcp. Путь к клону, если он другой, поправить в первой строке шага 2.

Токен хранится в одном из трёх режимов, он задаётся ключом `secrets` в конфиге:

- `keychain` — связка ключей macOS, рекомендуется: без пароля связки токен читает только установленный бинарь `mcp-confluence`, ни агент, ни другой процесс;
- `file` — файл `~/.config/mcp-confluence/secrets/token`, недоступный группе и остальным;
- `env` — переменная окружения `MCP_CONFLUENCE_TOKEN`; режим по умолчанию.

В режимах `file` и `env` агент с Bash может прочитать токен (README.md, «Токен»).

Блок, нужный не во всех режимах, помечен строкой перед ним: `keychain, file:`, `keychain:` или `env:`. Блок без метки — для всех режимов.

До шага 9 ни одна команда не обращается к Confluence. Первое обращение — ручная проверка шага 9 на тестовом поддереве.

## 1. Выбрать корневые страницы и узнать их id

Сервер работает в двух зонах (README.md, «Зоны»):

- зона записи — поддеревья корней записи (`write_roots`): агент создаёт в ней страницы и может переписать любую её страницу, включая сам корень. Обычно корень записи — отдельная страница, например «Claude drafts»;
- зона чтения — поддеревья корней чтения (`read_roots`, необязательны) вместе с зоной записи: разделы, которые агент только читает.

Один id не может быть в обоих списках.

Id страницы: у страниц с адресом вида `…/pages/viewpage.action?pageId=…` он виден сразу после `pageId=`. Иначе открыть страницу → «…» → «Информация о странице» — id в адресе после `pageId=`.

## 2. Собрать и установить

```bash
SCOPED_MCP_REPO=~/Projects/scoped-mcp
git -C "$SCOPED_MCP_REPO" log -1 --oneline
(cd "$SCOPED_MCP_REPO" && make install-mcp-confluence)
command -v mcp-confluence                 # ожидается тот же путь, что печатает следующая строка
echo "$(go env GOPATH)/bin/mcp-confluence"
```

Строка `git log` — коммит, из которого собран бинарь: записать его, при обновлении («Обновление mcp-confluence») изменения читаются от него. `make` печатает выполняемые команды с уже подставленным каталогом `$(go env GOPATH)/bin`, в том числе `codesign -s - -f -o runtime "<GOPATH>/bin/mcp-confluence"`; `codesign` при каждой установке печатает `…: replacing existing signature` — это не ошибка: бинарь Go уже подписан линкером, и `-f` заменяет эту подпись. Признак успеха — `make install-mcp-confluence` завершился с кодом 0, а подпись с hardened runtime видна так:

```bash
codesign -dv "$(go env GOPATH)/bin/mcp-confluence" 2>&1 | grep flags   # ожидается runtime в flags, вида flags=0x10002(adhoc,runtime)
```

Если `make install-mcp-confluence` остановился с `рабочее дерево не чистое: install только из закоммиченного состояния` — в scoped-mcp есть незакоммиченные или неотслеживаемые файлы: `git status`, закоммитить или убрать, повторить. Как устроена сборка и зачем подпись — README.md, «Хранение токена».

Если `command -v` ничего не печатает, каталог `$(go env GOPATH)/bin` не в `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
```

## 3. Создать конфиг

Блок создаёт конфиг, только если его ещё нет, и каталог для загрузок `~/agent-out`:

```bash
mkdir -p -m 700 ~/.config/mcp-confluence
mkdir -p ~/agent-out
[ -e ~/.config/mcp-confluence/config.yaml ] || cat > ~/.config/mcp-confluence/config.yaml <<'EOF'
base_url: https://confluence.example.com
secrets: keychain

read_roots: [123456789]
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
EOF
chmod 600 ~/.config/mcp-confluence/config.yaml
```

Затем открыть `~/.config/mcp-confluence/config.yaml` в редакторе:

1. заменить id в `read_roots` и `write_roots` на свои из шага 1 (`read_roots: []`, если корней чтения нет);
2. убрать ненужные инструменты; если выключены и `get_attachment`, и `upload_attachment` — удалить и раздел `attachments`;
3. для режима `file` или `env` заменить `secrets: keychain` на `secrets: file` или `secrets: env`.

`download_dir` создаётся сервером при первом скачивании. Каталоги `upload_dirs` должны существовать: несуществующий каталог из `upload_dirs` загрузка пропускает. Файл, которого нет (в том числе в несуществующем каталоге), отклоняется с `tools: path must be a non-empty absolute or ~/ path of an existing readable file …`; существующий файл, не лежащий ни в одном существующем каталоге из `upload_dirs`, — с `tools: the file is outside the directories allowed by attachments.upload_dirs`. Все правила конфига — README.md, «Конфиг».

## 4. Выпустить персональный токен (PAT)

В браузере: `https://confluence.example.com` → аватар → **Settings** → **Personal Access Tokens** → **Create token**. Имя `mcp-confluence`, срок жизни — по политике команды (ротация — раздел «Ротация токена»). Токен показывается один раз: сразу перейти к шагу 5, не закрывая страницу.

Токен действует от вашего имени: страницы создаются и правятся вашим пользователем, с вашими правами.

## 5. Сохранить токен

keychain, file:

```bash
mcp-confluence token
```

Запускать установленный бинарь (шаг 2), не `./bin/mcp-confluence`: элемент связки доверяет бинарю, который его создал.

keychain:

Если связки `~/Library/Keychains/scoped-mcp.keychain-db` ещё нет (её мог создать `mcp-tg login`), команда печатает `Creating keychain …: choose its password in the macOS dialog` и показывает системный диалог «Новый пароль связки». Придумать длинный уникальный пароль: он понадобится после каждой перезагрузки и после каждого `make install-mcp-confluence`.

keychain, file:

Затем `Confluence personal access token:` — вставить токен из шага 4 (ввод без эха), Enter. Ответ — `Token saved to <путь к связке или файлу>`. Повторный запуск перезаписывает токен. Ctrl+C отменяет ввод: токен не сохраняется, команда завершается с ошибкой `read input: context canceled`.

env:

Блок дописывает экспорт в `~/.zshrc`, если его там ещё нет:

```bash
if [ -f ~/.zshrc ] && grep -q '^export MCP_CONFLUENCE_TOKEN=' ~/.zshrc; then
  echo 'MCP_CONFLUENCE_TOKEN уже в ~/.zshrc'
else
  read -rs 'token?PAT: '; echo
  printf "export MCP_CONFLUENCE_TOKEN='%s'\n" "$token" >> ~/.zshrc
  unset token
fi
source ~/.zshrc
```

Токен лежит в `~/.zshrc` открытым текстом и виден Bash агента через окружение Claude Code.

## 6. Проверить сервер без Confluence

Сначала — что конфиг и токен читаются:

```bash
mcp-confluence serve </dev/null && echo OK
```

Из `/dev/null` сервер сразу получает конец ввода и завершается: если конфиг и токен в порядке, печатается `OK`, иначе — ошибка (раздел «Если что-то не работает»). Действует ли токен в Confluence, эта команда не проверяет.

Затем — какие инструменты видны. Запрос `tools/list` в Confluence не ходит:

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 3; } | mcp-confluence serve
```

Приходят две строки JSON; во второй (`"id":2`) в `result.tools[].name` — имена включённых инструментов. `sleep` держит stdin открытым: на конце ввода MCP SDK закрывает соединение, и ответы на ещё не обработанные запросы теряются.

## 7. Подключить к Claude Code

Сервер не запускается вручную: Claude Code сам запускает `mcp-confluence serve` при старте сессии и завершает его при выходе. Выберите один из двух вариантов.

### 7а. Для всех, кто работает с проектом: `.mcp.json`

Подходит для всех режимов; для `env` — единственный вариант. Добавьте запись в `mcpServers` файла `.mcp.json` в корне проекта. Запись ниже — вариант env, он работает и в режимах keychain и file.

keychain, file:

```json
"scoped-confluence": {
  "type": "stdio",
  "command": "mcp-confluence",
  "args": ["serve"]
}
```

env:

Токен подставляется из окружения каждого:

```json
"scoped-confluence": {
  "type": "stdio",
  "command": "mcp-confluence",
  "args": ["serve"],
  "env": {"MCP_CONFLUENCE_TOKEN": "${MCP_CONFLUENCE_TOKEN:-}"}
}
```

Пустое значение по умолчанию в `${MCP_CONFLUENCE_TOKEN:-}` обязательно. Без него при незаданной переменной Claude Code передаёт серверу буквальный текст `${MCP_CONFLUENCE_TOKEN}`, сервер принимает его за токен, и каждый вызов отвечает `the token was rejected`. С пустым значением сервер не стартует и печатает `mcp-confluence: config: token is not available: secret: not found: MCP_CONFLUENCE_TOKEN is not set`.

### 7б. Только для себя: `claude mcp add`

Только для режимов `keychain` и `file`. Запись попадает в `~/.claude.json`, репозиторий не меняется:

```bash
cd /path/to/project
claude mcp add scoped-confluence -- mcp-confluence serve
```

## 8. Запустить Claude Code

Перед запуском, по режиму:

- keychain: после перезагрузки сначала разблокировать связку (раздел «После перезагрузки»);
- env: запускать из терминала, где загружен `~/.zshrc`.

Для всех режимов: перейти в каталог проекта из шага 7 и запустить Claude Code:

```bash
cd /path/to/project && claude
```

Вариант 7а: на вопрос о сервере `scoped-confluence` из `.mcp.json` — разрешить. В `/mcp` у `scoped-confluence` статус connected.

## 9. Проверить на тестовом поддереве (первое обращение к Confluence)

Поведение Confluence в этих пунктах по документации Atlassian не подтверждено, а тесты сервера его не ловят: ответы Confluence в них написаны по документации. У каждого риска указаны симптом, причина и место в коде. При расхождении записать пункт и симптом и продолжить; правка кода — отдельной задачей.

### Подготовка (в браузере)

Под корнем записи создать руками:

- страницу «mcp-confluence test» (дальше — тестовая) со словом `zqxprobe` в тексте;
- под ней страницу «mcp-confluence test child» (дальше — внук корня);
- на тестовой странице — комментарий, ответ на него и ответ на ответ;
- на тестовой странице — небольшое вложение (`.txt` или `.png`).

Выписать id:

- корня записи;
- тестовой страницы;
- внука корня;
- страницы вне обеих зон, лучше в том же пространстве и со словом `zqxprobe` в тексте;
- родителя корня записи, если он вне зоны чтения (если он в зоне чтения, в 9.4 вместо него берётся страница вне обеих зон из пункта выше);
- один из id `read_roots` (если `read_roots` пуст — пункт 9.8 пропустить).

Просить Claude обычным текстом, подставляя id вместо `<…>`, и сверять ответ инструмента с ожидаемым.

### Если пункт не прошёл: повтор вызова в терминале

Детали неудачного запроса сервер пишет в stderr, которого в Claude Code не видно. Повторить вызов в терминале, подставив инструмент и аргументы:

```bash
TOOL=get_page ARGS='{"page_id":"123456789"}'
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$TOOL\",\"arguments\":$ARGS}}"
  sleep 15; } | mcp-confluence serve
```

`sleep 15` держит stdin открытым, пока сервер ждёт ответа Confluence: без него соединение закроется раньше ответа. Если строки с `"id":2` нет — увеличить паузу.

- stdout — две строки JSON; во второй (`"id":2`) результат инструмента или `"isError":true` с текстом ошибки, как его видит агент;
- stderr — строки вида `mcp-confluence: <дата> <время> GET /rest/api/content/…: status 404`: метод, путь и статус или класс сетевой ошибки, без тела ответа.

### 9.1. Чтение и parent_id

1. «Прочитай страницу <id корня записи>» → `title`, `version`, `body`, `webui`; без `parent_id`, если корень записи не лежит внутри `read_roots`.
2. «Прочитай страницу <id тестовой страницы>» → `parent_id` равен id корня записи. Если корень записи — страница верхнего уровня пространства, у тестовой страницы единственный предок — корень, и этот пункт от порядка `ancestors` не зависит. Если корень записи лежит глубже, при обратном порядке `ancestors` `parent_id` здесь пропадёт (см. риск ниже).
3. «Прочитай страницу <id внука корня>» → `parent_id` равен id тестовой страницы.
4. «Покажи детей страницы <id корня записи>» → в списке тестовая страница.

Риск — порядок `ancestors` у страницы:

- Симптом: `parent_id` нет в пунктах 2 и 3; если корень записи — страница верхнего уровня пространства, в пункте 3 `parent_id` может быть равен id корня записи.
- Причина: Confluence отдаёт `ancestors` от ближайшего предка к дальнему, а сервер считает родителем последний элемент. Проверка зоны в guard от порядка не зависит.
- Где менять: функция `pageOf` в `confluence/pages.go`.

### 9.2. Комментарии

«Покажи комментарии страницы <id тестовой страницы>» → три комментария: у первого `parent_comment_id` нет, у ответа он равен id первого, у ответа на ответ — id ответа.

Риск — родительских комментариев нет в `ancestors`:

- Симптом: у ответов `parent_comment_id` пустой.
- Причина: в `ancestors` комментария на этом Confluence нет родительских комментариев.
- Где менять: `parentCommentID` и `commentExpand` в `confluence/comments.go`, разрешённый `expand` комментариев (`commentExpand`) в `guard/read.go`.

Риск — порядок `ancestors` у комментария:

- Симптом: у ответа на ответ `parent_comment_id` равен id первого комментария.
- Причина: `ancestors` идут от ближайшего предка к дальнему, а сервер берёт последний предок типа `comment` (обход `slices.Backward`).
- Где менять: развернуть обход в `parentCommentID` в `confluence/comments.go`.

### 9.3. Поиск

1. «Найди страницы со словом zqxprobe» → тестовая страница есть, страницы вне зоны нет.
2. «Найди "zqxprobe"» — Claude должен передать в `query` слово вместе с кавычками → результат или пустой список без ошибки.

Риск — экранирование кавычки в CQL:

- Симптом: пункт 2 отвечает `Confluence rejected the request`.
- Причина: Confluence Data Center не принимает `\"` внутри `text ~ "…"`; в документации DC правил экранирования нет, принято по документации Cloud.
- Где менять: `cqlStringEscaper` в `guard/cql.go`; `tools` берут CQL оттуда же (`guard.SearchCQL`).

### 9.4. Отказ вне зоны

«Прочитай страницу <id вне зон>», «Покажи детей страницы <id вне зон>», «Покажи комментарии страницы <id родителя корня записи или, если он в зоне чтения, страницы вне обеих зон>» → каждый раз ровно `page not found`, без названия страницы.

### 9.5. Вложения

1. «Покажи вложения страницы <id тестовой страницы>» → вложение с `id` вида `att<число>` или `<число>`, `size` больше 0, `version` не меньше 1 и непустым `media_type`.
2. «Скачай это вложение» → `path` внутри `download_dir`; файл открывается и совпадает с вложением в браузере.
3. «Скачай вложение <id из пункта 1 без префикса att> страницы <id тестовой страницы>» → тот же `path`, файл тот же.

Риск — поля вложения без `expand`:

- Симптом: в пункте 1 `size: 0` или пустой `media_type`.
- Причина: список вложений запрашивается с `expand=version`, и этот Confluence без отдельного `expand` не отдаёт `extensions.fileSize` или `extensions.mediaType`. `version` от этого риска не зависит: он запрошен явно.
- Где менять: значение `attachmentListExpand` в `confluence/attachments.go` и список разрешённых значений `attachmentListExpand` в `guard/read.go` — в обоих одновременно; правила детей страницы (`listQuery`) не трогать.

Риск — формат id вложения:

- Симптом: в пункте 1 `id` не вида `att<число>` и не `<число>`; пункты 2 и 3 отвечают `tools: attachment_id must be an id from list_attachments …`; в 9.7 пункт 2 отвечает `Confluence is unavailable, retry later`, а в stderr — `listed attachment has an unrecognized id`. Если `id` в списке правильный, а 9.7 пункт 2 отвечает `Confluence is unavailable, retry later` и в stderr — `POST …/data: transport: guard: zone check failed, the request was not sent: response id differs from …`, ответ проверки зоны вложения содержит `id` другой формы.
- Причина: сервер принимает id вложения как `<число>` или `att<число>`, путь `…/{att}/data` и проверку зоны строит по числовой части и сверяет с ней `id` из ответа проверки зоны; этот Confluence отдаёт id в другой форме.
- Где менять: `AttachmentContentID` в `guard/guard.go` — одно место нормализации для клиента (`UploadAttachment` в `confluence/attachments.go`), инструментов (`checkAttachmentID` в `tools/toolset.go`) и проверки зоны вложения (`sameAttachmentID` в `guard/zone.go`).

Риск — servlet скачивания:

- Симптом: список работает, а пункт 2 отвечает `the token was rejected`, `page not found` или `Confluence is unavailable, retry later` (например, на редирект на страницу входа), или сохранённый файл — HTML-страница входа.
- Причина: servlet `/download/attachments/` не принимает Bearer PAT или требует `modificationDate`; это не задокументировано, URL сервер строит сам, без `modificationDate`.
- Где менять: `DownloadAttachment` в `confluence/attachments.go`, правила `downloadQuery` в `guard/read.go`.

### 9.6. Создание и правка

1. «Создай под страницей <id корня записи> страницу "mcp-confluence test 2" с текстом `<p>test</p>`» → `id`, `version: 1`, `webui`; страница видна в браузере под корнем записи.
2. «Обнови её: тот же заголовок, текст `<p>test 2</p>`, версия 1» → `version: 2`. Затем «Прочитай её» → `parent_id` равен id корня записи (если пункт 9.1.2 прошёл), и «Покажи детей страницы <id корня записи>» → она в списке; в браузере она осталась под корнем записи.
3. «Обнови её ещё раз с версией 1» → `the page was changed since it was read; read it again`; в браузере версия осталась 2.
4. «Создай под страницей <id корня записи> ещё одну страницу "mcp-confluence test 2"» → `this title is already taken in the space`.

Риск — `space` в `PUT`:

- Симптом: пункт 2 отвечает `Confluence rejected the request`; пункт 3 тогда не проверить.
- Причина: Data Center требует `space` в `PUT` (справочник 8.5.4 упоминает 400 «if no space»), а сервер шлёт `PUT` без `space`, и guard такой ключ запрещает.
- Где менять: разрешить в guard `space.key`, равный пространству страницы из проверки зоны (`guard/write.go`), и слать его из клиента (`updatePageBody` в `confluence/pages.go`).

Риск — `PUT` без `ancestors` переносит страницу:

- Симптом: после пункта 2 у страницы нет `parent_id` или он другой, среди детей корня записи её нет, в браузере она в корне пространства или под другим родителем; дальнейшие вызовы с её id отвечают `page not found` — она вне зоны.
- Причина: сервер шлёт `PUT` без `ancestors` (guard другие ключи запрещает), а этот Confluence трактует их отсутствие как перенос страницы на верхний уровень пространства. Страницу вернуть под корень записи руками в браузере.
- Где менять: слать `ancestors` с текущим родителем в `updatePageBody` (`confluence/pages.go`) и разрешить в `checkUpdateBody` (`guard/write.go`) ровно один `ancestors[{id}]`, равный родителю страницы из проверки зоны, — так перенос по-прежнему невозможен.

Риск — ответ на устаревшую версию:

- Симптом: пункт 3 отвечает `Confluence rejected the request`.
- Причина: на устаревшую версию Confluence отвечает не 409. Агент всё равно получает отказ, страница не изменена.
- Где менять: `pageWriteRejection` в `confluence/pages.go`.

Риск — признак занятого заголовка:

- Симптом: пункт 4 отвечает `Confluence rejected the request`.
- Причина: текст отказа на этом Confluence не начинается с `A page with this title already exists`. Это допустимо: агент получает менее точный отказ.
- Где менять: константа `titleTakenMessagePrefix` в `confluence/pages.go`.

### 9.7. Загрузка вложения

```bash
printf 'mcp-confluence upload test\n' > ~/agent-out/mcp-confluence-test.md
```

1. «Приложи ~/agent-out/mcp-confluence-test.md к странице <id тестовой страницы>» → `id`, `title: mcp-confluence-test.md`, `version: 1`; вложение видно в браузере.
2. Повторить → тот же `id`, `version: 2`; в браузере одно вложение с двумя версиями.
3. «Приложи ~/.zshrc к странице <id тестовой страницы>» → `tools: the file is outside the directories allowed by attachments.upload_dirs`; в Confluence ничего не приложено.

Риск — значение `X-Atlassian-Token`:

- Симптом: пункт 1 отвечает `page not found` или `Confluence rejected the request`, а `get_page` той же страницы работает.
- Причина: Confluence не принимает `X-Atlassian-Token: no-check` (в справочнике — `nocheck`); отказ XSRF приходит как 403, а 403 сервер показывает агенту как `page not found`.
- Где менять: константа `atlassianTokenNoCheck` в `guard/upload.go` и в `confluence/attachments.go` — в обоих одновременно.

Риск — `container` у вложения:

- Симптом: пункт 1 прошёл, а пункт 2 отвечает `page not found`.
- Причина: перед загрузкой новой версии guard проверяет, что вложение принадлежит странице, по полю `container` из ответа проверки зоны (`expand=ancestors,space,container`); этот Confluence его не отдаёт.
- Где менять: `requireAttachmentOfPage` и `isAttachedToPage` в `guard/upload.go`, `zoneCheckExpand` в `guard/zone.go`.

### 9.8. Отказ записи вне зоны записи

«Обнови страницу <id из read_roots>», «Создай страницу под страницей <id из read_roots>», «Приложи ~/agent-out/mcp-confluence-test.md к странице <id из read_roots>» → каждый раз `page not found`; в браузере страница не изменилась.

### Уборка

Тестовые страницы и вложения удалить руками в Confluence: сервер удалять не умеет. Удалить скачанные файлы в `download_dir` и тестовый файл:

```bash
rm ~/agent-out/mcp-confluence-test.md
```

## Изменить корни, инструменты или вложения

Поправить `~/.config/mcp-confluence/config.yaml`, проверить `mcp-confluence serve </dev/null && echo OK` (шаг 6), затем в Claude Code `/mcp` → `scoped-confluence` → Reconnect.

## После перезагрузки (только keychain)

Предпочтительно — один раз в своём терминале, до запуска Claude Code:

```bash
security unlock-keychain ~/Library/Keychains/scoped-mcp.keychain-db
```

Иначе при подключении MCP появится диалог разблокировки связки. Вводить пароль в него можно, только если подключение вызвали вы сами (запуск Claude Code, Reconnect в `/mcp`). Диалог пароля связки, который вы не вызывали, — «Отменить»: такое окно может показать любой процесс, чтобы выманить пароль.

Без GUI-сессии (ssh) запуск с заблокированной связкой завершается ошибкой `keychain: user interaction is not allowed (no GUI session or keychain is locked)` или `keychain: access denied` — разблокировать связку в GUI-сессии.

## Обновление mcp-confluence

1. `git pull` в scoped-mcp; прочитать изменения в `mcp-confluence` и `pkg/secret` от коммита, записанного в шаге 2, — собирать только из просмотренного коммита.
2. `make install-mcp-confluence` (шаг 2) — только из чистого дерева scoped-mcp.
3. keychain: при первом обращении к токену пересобранный бинарь получит диалог доступа к элементу связки — «mcp-confluence хочет использовать… введите пароль связки scoped-mcp». Ввести пароль связки и нажать «Разрешать всегда». Диалог в любой другой момент, не сразу после своего `make install-mcp-confluence`, — «Отказать» и переустановить из просмотренного коммита (`git log -1`, `make install-mcp-confluence`).
4. `/mcp` → `scoped-confluence` → Reconnect.

## Ротация токена

1. Выпустить новый PAT (шаг 4). Старый пока не отзывать.
2. Переключить сервер на новый токен:
   - keychain, file: `mcp-confluence token` (перезаписывает), затем `/mcp` → `scoped-confluence` → Reconnect;
   - env: поправить строку `export MCP_CONFLUENCE_TOKEN=` в `~/.zshrc`, `source ~/.zshrc`, выйти из Claude Code и запустить его заново из этого терминала: окружение сервера наследуется от запущенного Claude Code, Reconnect его не обновит.
3. В Confluence отозвать старый токен (**Settings** → **Personal Access Tokens** → **Revoke**).

При подозрении на утечку сначала отозвать старый токен, потом выпустить новый и переключиться.

## Если что-то не работает

Ошибки запуска печатаются в stderr с префиксом `mcp-confluence:`, код выхода 1. Ошибки инструментов модель получает ответом.

| Сообщение | Что делать |
|---|---|
| `mcp-confluence: command not found` | Шаг 2. |
| `usage: mcp-confluence [--config path] serve\|token` | Неверные аргументы: подкоманда одна, `serve` или `token`; флаг `--config` пишется до неё. |
| `scoped-confluence` в `/mcp` — failed | `mcp-confluence serve </dev/null` в терминале покажет ошибку конфига или токена. |
| `read config: open …: no such file or directory` | Конфига нет: шаг 3, или путь в `--config` неверен. |
| `config: cannot decode YAML into config` | Невалидный YAML или неизвестный ключ (опечатка); в `tools` допустимы только девять флагов из README. |
| `config: base_url must be an absolute https URL without userinfo, query and fragment` | `https://host[/путь]` без логина, `?` и `#`. |
| `config: secrets must be env, file or keychain` | Опечатка в `secrets`. С `keychain: not supported on this platform` — режим `keychain` есть только на macOS. |
| `config: tools must enable at least one tool` | Нет раздела `tools` или все флаги `false`. |
| `config: tools.create_page, tools.update_page and tools.upload_attachment require a non-empty write_roots` | Инструмент записи включён, `write_roots` пуст. |
| `config: read tools require at least one id in read_roots or write_roots` | Инструмент чтения включён, оба списка корней пусты. |
| `config: root ids must be positive` | В списке корней ноль или отрицательное число. |
| `config: root id repeats within read_roots, within write_roots or across them` | Один id дважды или в обоих списках: корень записи и так в зоне чтения. |
| `config: the attachments section must be set exactly when tools.get_attachment or tools.upload_attachment is enabled` | Раздел `attachments` есть без этих инструментов или они включены без него. |
| `config: attachments.download_dir must be absolute or start with ~/ and must not be the home directory, its parent or the root` | Каталог относительный, `~`, его предок, корень или симлинк на них: выбрать подкаталог, например `~/agent-out/confluence`. |
| `config: attachments.upload_dirs must be non-empty, each absolute or starting with ~/, none giving access to the home directory` | То же для `upload_dirs`, или список пуст. |
| `config: attachments.extensions must be non-empty, each like .md, without / and not repeated in any letter case` | Поправить список расширений. |
| `config: attachments.max_bytes must be between 1 and 20971520` | Предел — до 20 MiB. |
| `config: search.max_results must be between 1 and 100` | Поправить `search.max_results`. |
| `config: home directory is unknown, ~ cannot be expanded` | В окружении нет `HOME`: запускать из обычного терминала или писать абсолютные пути. |
| `config: token is not available: secret: not found: MCP_CONFLUENCE_TOKEN is not set` | env: переменная не задана в окружении Claude Code: `source ~/.zshrc`, запустить Claude Code из этого терминала, проверить блок `env` в `.mcp.json` (шаг 7а). |
| `config: token is not available: …secret: not found…` в режиме `file` или `keychain` | Токен не сохранён: `mcp-confluence token`. |
| `config: token is not available: keychain: no such keychain…` | Связки нет или `keychain_path` другой: `mcp-confluence token` создаст её. |
| `config: token is not available: file: secret must not be accessible by group or others…` | `chmod 700 ~/.config/mcp-confluence/secrets`, файлу `token` — `chmod 600`. |
| `config: token is not available: token is empty` | Сохранён пустой токен: `mcp-confluence token`. |
| `keychain: access denied` | Отказ в диалоге связки или заблокированная связка без GUI. Отказали в диалоге разблокировки — Reconnect и ввести пароль. Отказали в ожидаемом диалоге доступа сразу после своего `make install-mcp-confluence` — Reconnect, ввести пароль связки, «Разрешать всегда». Диалог, которого не ждали, — пароль не вводить, переустановить из просмотренного коммита. |
| `keychain: user interaction is not allowed (no GUI session or keychain is locked)` | Запуск без GUI-сессии (ssh) с заблокированной связкой: разблокировать её в GUI-сессии. |
| `token: secrets mode is env, set MCP_CONFLUENCE_TOKEN in the environment instead` | `mcp-confluence token` в режиме `env`: токен задаётся переменной, шаг 5 (env). |
| `token: empty input` | Пустой ввод, токен не сохранён: повторить. |
| `read input: context canceled` | Ввод прерван Ctrl+C, токен не сохранён. |
| `token: hiding terminal input is supported only on macOS, pipe the token into stdin instead` | Не macOS: подать токен через канал, например из менеджера секретов: `… \| mcp-confluence token`. |
| `рабочее дерево не чистое: install только из закоммиченного состояния` | `make install-mcp-confluence` на дереве с незакоммиченными или неотслеживаемыми файлами: `git status` в scoped-mcp, закоммитить или убрать, повторить. |
| Диалог доступа к элементу связки не сразу после своего `make install-mcp-confluence` | Бинарь пересобран без вашего ведома или подменён: «Отказать», переустановить из просмотренного коммита. |
| Инструмента нет в списке | Он не включён в `tools` (шаг 3) или сервер не переподключён после правки конфига. |
| `the token was rejected` | PAT истёк или отозван: «Ротация токена». Если токен точно действует — в `.mcp.json` стоит `${MCP_CONFLUENCE_TOKEN}` без `:-`, и сервер получил буквальный текст вместо токена (шаг 7а). |
| `page not found` на странице, которая открывается в браузере | Страница не в зоне: проверить корни в конфиге, для записи — `write_roots`. Блог-посты, черновики и страницы в корзине не поддерживаются. Для `get_attachment` — вложения нет у названной страницы. |
| `page not found` на `upload_attachment`, когда `get_page` той же страницы работает | Риск `X-Atlassian-Token` или `container` у вложения: шаг 9.7. |
| `tools: attachment_id must be an id from list_attachments …` | `attachment_id` не взят из `list_attachments`, или Confluence отдаёт id вложений в неожиданной форме: риск «формат id вложения», шаг 9.5. |
| `Confluence is unavailable, retry later` | Нет сети или VPN, Confluence отвечает 5xx или редиректом. Повтор безопасен. Для `list_comments` и `list_attachments` — ещё и список длиннее 2000 элементов. |
| `the change may have been applied; check the page before retrying` | Ответ на запись не получен или неожиданный: открыть страницу в браузере, повторять только если изменения нет. |
| `Confluence rejected the request` | Confluence отклонил запрос (4xx). При первой настройке — сверить с рисками шага 9. |
| `request is not allowed` | Guard отверг запрос самого сервера, запрос не отправлялся: дефект сервера. Метод и путь — в stderr (шаг 9, «Если пункт не прошёл: повтор вызова в терминале»). |
| `tools: the file is outside the directories allowed by attachments.upload_dirs` | Файл после раскрытия симлинков не лежит ни в одном существующем каталоге из `upload_dirs` (несуществующие каталоги пропускаются): положить файл в существующий каталог из `upload_dirs` — при необходимости создать каталог и перенести файл. |
| `tools: the file is reached from its attachments.upload_dirs entry through the home directory or the root: the entry gives access to the home directory` | Путь от каталога `upload_dirs` к файлу проходит через домашний каталог или корень (каталог в конфиге — симлинк на них или выше): указать в конфиге каталог внутри домашнего и класть файлы туда. |
| `tools: the path is not a regular file` | Указан каталог, канал или устройство. |
| `tools: the file extension is not allowed by attachments.extensions` | Переименовать файл или добавить расширение в конфиг. |
| `tools: the file is larger than attachments.max_bytes` | Файл для загрузки больше `max_bytes`. |
| `tools: the file was replaced or changed after the check and was not sent` | Файл подменили, удалили или он вырос между проверкой и чтением: проверить, что лежит по пути, и повторить. |
| `the attachment is larger than attachments.max_bytes` | Скачиваемое вложение больше `max_bytes`: увеличить `max_bytes` (до 20 MiB) или скачать в браузере. |
| `tools: the attachment could not be saved into attachments.download_dir` | Каталог `download_dir` не создаётся или в него нельзя писать; причина — после текста. Проверить права каталога. |
| `tools: the file is the config file, the token file or the keychain` | Конфиг, файл токена и связку нельзя ни загрузить, ни перезаписать скачиванием. |
| `tools: the attachment title is not a safe file name …` | Имя вложения нарушает правило имён (README.md, «Имена вложений»): через MCP его не скачать, только в браузере. |
| `tools: path must be a non-empty absolute or ~/ path of an existing readable file …` | Путь пустой, относительный, файла или его каталога нет (в том числе каталога из `upload_dirs`), или имя файла нарушает правило имён. |
