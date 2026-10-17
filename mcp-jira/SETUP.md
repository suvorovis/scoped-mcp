# Настройка mcp-jira: от токена до работы в Claude Code

Команды копируются блоками и выполняются по порядку в обычном терминале (zsh). Через `!` в Claude Code этого делать нельзя: префикс `!` выполняет команду из окна Claude Code, и её ввод и вывод попадают в контекст агента, а токен вводится руками и попадать туда не должен.

Нужны: Go не ниже версии из `go.mod` в корне scoped-mcp (`go 1.26.0`), Claude Code CLI (`claude`) и клон репозитория scoped-mcp. Путь к клону, если он другой, поправить в первой строке шага 1.

Режим хранения токена задаётся ключом `secrets` в конфиге:

- `keychain` — связка ключей macOS, рекомендуется: без пароля связки токен читает только установленный бинарь `mcp-jira`, ни агент, ни другой процесс;
- `file` — файл `~/.config/mcp-jira/secrets/token`, недоступный группе и остальным;
- `env` — переменная окружения `MCP_JIRA_TOKEN`; режим по умолчанию.

В режимах `file` и `env` агент с Bash может прочитать токен (см. «Ограничение: агент может прочитать токен»).

Блок, нужный не во всех режимах, помечен жирной строкой перед ним: **keychain, file:**, **keychain:** или **env:**. Метка действует до следующей метки или до конца шага. Блок без метки — для всех режимов.

До шага 7 ни одна команда не обращается к Jira.

## 1. Собрать и установить

```bash
SCOPED_MCP_REPO=~/Projects/scoped-mcp
git -C "$SCOPED_MCP_REPO" log -1 --oneline
(cd "$SCOPED_MCP_REPO" && make install-mcp-jira)
command -v mcp-jira                   # ожидается тот же путь, что печатает следующая строка
echo "$(go env GOPATH)/bin/mcp-jira"
```

Строка `git log` — коммит, из которого собран бинарь: записать его, при обновлении («Обновление mcp-jira») изменения читаются от него. `make` печатает выполняемые команды с уже подставленным каталогом установки, в том числе `codesign -s - -f -o runtime "<GOPATH>/bin/mcp-jira"`; `codesign` при каждой установке печатает `…: replacing existing signature` — это не ошибка: бинарь Go уже подписан линкером, и `-f` заменяет эту подпись. Признак успеха — `make install-mcp-jira` завершился с кодом 0, а подпись с hardened runtime видна так:

```bash
codesign -dv "$(go env GOPATH)/bin/mcp-jira" 2>&1 | grep flags   # ожидается runtime в flags, вида flags=0x10002(adhoc,runtime)
```

Если `make install-mcp-jira` остановился с `рабочее дерево не чистое: install только из закоммиченного состояния` — в scoped-mcp есть незакоммиченные или неотслеживаемые файлы: `git status`, закоммитить или убрать, повторить. Как устроена сборка и зачем подпись — [README, «Хранение токена»](README.md#хранение-токена).

Если `command -v` ничего не печатает, каталог `$(go env GOPATH)/bin` не в `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
```

## 2. Создать конфиг для PROJ

Команда создаёт конфиг, только если его ещё нет. В строке `secrets:` указать выбранный режим: `keychain`, `file` или `env`:

```bash
mkdir -p ~/.config/mcp-jira && chmod 700 ~/.config/mcp-jira
[ -e ~/.config/mcp-jira/config.yaml ] || cat > ~/.config/mcp-jira/config.yaml <<'EOF'
base_url: https://jira.example.com
secrets: keychain

tools: {create_issue: true, attach_files: true, add_comment: true, link_issues: true, update_issue: true}

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
EOF
cat ~/.config/mcp-jira/config.yaml
```

Пути хранилищ по умолчанию — `~/.config/mcp-jira/secrets` для `file` и `~/Library/Keychains/scoped-mcp.keychain-db` для `keychain` (общая связка с другими MCP-серверами scoped-mcp); другой путь задаётся ключом `secrets_dir` или `keychain_path`. Ключа `token_path` больше нет: конфиг с ним не загружается (`config: malformed YAML`), переход — в разделе «Переход с env или token_path на keychain».

Раздел `tools` включает инструменты: `create_issue` — создание задач, `attach_files` — вложения, `add_comment` — комментарии, `link_issues` — связи, `update_issue` — изменение полей из `update_fields`; все — для задач проектов из `projects`. Хотя бы один должен быть включён. **Конфиг без раздела `tools` после обновления mcp-jira не загружается** (`config: tools must enable at least one tool`): в старый конфиг добавить раздел `tools` руками — команда выше существующий конфиг не трогает: `tools: {create_issue: true, attach_files: true}`, если в конфиге уже есть раздел `attachments`, иначе `tools: {create_issue: true}` (`attach_files: true` без раздела `attachments` не загружается). Это возвращает только прежние инструменты; комментарии, связи и изменение полей — отдельное явное включение: дописать в `tools` `add_comment: true`; для связей — `link_issues: true` и раздел `issue_links`; для изменения — `update_issue: true` и `update_fields` у проекта.

`assign_to_self: true` назначает создаваемые задачи на владельца токена, то есть на вас. Без него Jira ставит исполнителя по умолчанию для проекта — в PROJ это другой человек. Если конфиг был создан раньше, команда выше его не тронет: строку добавить руками.

Раздел `attachments` задаёт правила для `attach_files`: приложить к задаче PROJ файл из `~/agent-out` (вместе с подкаталогами) с одним из перечисленных расширений и не больше 10 MiB (`10485760` байт). `tools.attach_files: true` и раздел `attachments` задаются только вместе: убрать вложения — убрать и флаг, и раздел. Если конфиг был создан раньше, раздел добавить руками.

Раздел `issue_links` задаёт фразы `relation`, которые модель передаёт в `link_issues`, и тип связи Jira за каждой: `type` — имя типа связи; `direction: outward` — фраза совпадает с outward-описанием типа (у `Blocks` — «blocks»: `issue_key` блокирует `target_key`), `direction: inward` — с inward-описанием («is blocked by»: `issue_key` заблокирована `target_key`). У симметричного `Relates` направление ни на что не влияет, пишите `outward`. Описания типов: у `Blocks` outward — «blocks», inward — «is blocked by»; у `Relates` обе стороны — «relates to». `tools.link_issues: true` и непустой раздел `issue_links` задаются только вместе. Если конфиг был создан раньше, флаг и раздел добавить руками.

`update_fields: [epic_link]` у PROJ разрешает `update_issue` менять у задач PROJ поле `epic_link` (Epic Link, `customfield_10201`): переносить задачу в другой эпик и убирать из эпика. Ключи `update_fields` берутся из того же словаря `fields`, что и поля типов: `fields` общий для `create_issue` и `update_issue`. `tools.update_issue: true` и непустой `update_fields` хотя бы у одного проекта задаются только вместе. Ключ `issue_key` в `fields` зарезервирован: это параметр `update_issue` с ключом изменяемой задачи. Если конфиг был создан раньше, флаг и `update_fields` добавить руками.

Чтобы разрешить другой проект, тип, поле или отношение:

- проект или тип — добавить в `projects` запись с ключом проекта, именем и ID типа; ID типа — через `jira_get_project_issue_types` в MCP `jira-readonly`;
- каталог или расширение для вложений — дописать в `attachments.dirs` или `attachments.extensions`; предел `max_bytes` — не больше 20 MiB; каталог не должен давать доступ к домашнему каталогу: корень, `~`, `~/..`, `/Users` отклоняются при загрузке конфига, а файл из домашнего каталога, видимый через такой каталог под другим путём (например, через `/System/Volumes` на macOS), — при вызове инструмента;
- поле — сначала описать его в `fields` (ID кастомного поля — через `jira_search_fields` в MCP `jira-readonly`), потом добавить в список `fields` типа: поле, которого нет в `fields`, разрешить типу нельзя;
- изменяемое поле — так же описать его в `fields` и добавить в `update_fields` проекта;
- отношение — дописать в `issue_links` фразу, тип и направление; имена типов связей и их outward/inward — через `jira_get_link_types` в MCP `jira-readonly`;
- формат, виды полей и правила валидации — в [README](README.md#конфиг).

## 3. Выпустить персональный токен (PAT)

В Jira: аватар → **Profile** → **Personal Access Tokens** → **Create token**.

| Поле | Значение |
|---|---|
| Token name | `mcp-jira` |
| Automatic expiry | включить, срок — 90 дней (или короче по политике команды) |

Jira покажет токен только один раз: не закрывая страницу, перейти к шагу 4. Токен действует от вашего имени: задачи создаются вашим пользователем, с вашими правами.

## 4. Сохранить токен

**keychain, file:**

```bash
mcp-jira token
```

Команда печатает `Jira personal access token:` — вставить токен из шага 3 (ввод без эха), Enter. Ответ — `Token saved to <путь к связке или файлу>`. Повторный запуск перезаписывает токен. Пустой ввод (`token: empty input`) токен не трогает. Ctrl+C отменяет ввод: токен не сохраняется, команда завершается с ошибкой `read input: context canceled`.

**keychain:**

Запускать установленный бинарь (шаг 1), не `./bin/mcp-jira`: элемент связки доверяет бинарю, который его создал.

Если связки `~/Library/Keychains/scoped-mcp.keychain-db` ещё нет (её мог создать другой MCP, например `mcp-tg login`, `mcp-gitlab token` или `mcp-confluence token`), до приглашения ввода токена команда печатает `Creating keychain …: choose its password in the macOS dialog` и показывает системный диалог «Новый пароль связки». Придумать длинный уникальный пароль: он понадобится после каждой перезагрузки и после каждого `make install-mcp-jira`. Пока этот диалог открыт, Ctrl+C не прерывает `mcp-jira token` — команда завершится только после закрытия диалога.

**env:**

Перед запуском учесть:

- токен ляжет в `~/.zshrc` открытым текстом. Этот файл обычно читается всеми (`0644`) и нередко хранится в git вместе с дотфайлами — проверить, что токен не уйдёт в репозиторий;
- Bash агента наследует окружение Claude Code: токен виден через `env`/`echo $MCP_JIRA_TOKEN` и может попасть в вывод случайно.

Блок дописывает экспорт в `~/.zshrc`, если его там ещё нет:

```bash
if [ -f ~/.zshrc ] && grep -q '^export MCP_JIRA_TOKEN=' ~/.zshrc; then
  echo 'MCP_JIRA_TOKEN уже в ~/.zshrc'
else
  read -rs 'token?PAT: '; echo
  printf "export MCP_JIRA_TOKEN='%s'\n" "$token" >> ~/.zshrc
  unset token
fi
source ~/.zshrc
```

Как сервер получает переменную: Claude Code запускает `mcp-jira serve` и передаёт ему переменные из поля `env` записи в `.mcp.json` (шаг 6.1). Значение `${MCP_JIRA_TOKEN:-}` там подставляется из окружения самого Claude Code — того, что было в терминале при его запуске. Поэтому новое значение переменной сервер увидит только после перезапуска Claude Code из терминала, где оно уже задано; Reconnect в `/mcp` перезапускает сервер, но окружение Claude Code не меняет. Claude Code запускать из терминала, где `~/.zshrc` загружен (шаг 6.3), сервер подключать через `.mcp.json` (шаг 6.1).

## 5. Проверить сервер без Claude Code

Сначала — что конфиг и токен читаются:

```bash
mcp-jira serve </dev/null && echo OK
```

Из `/dev/null` сервер сразу получает конец ввода и завершается: если конфиг и токен в порядке, печатается `OK`, иначе — ошибка (таблица в конце). Действует ли токен в Jira, эта команда не проверяет.

Затем — что инструмент виден. Запрос `tools/list` в Jira не ходит, задача не создаётся:

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 3; } | mcp-jira serve
```

Приходят две строки JSON; во второй (`"id":2`) — инструменты `add_comment`, `attach_files`, `create_issue`, `link_issues` и `update_issue`; в описании `create_issue` перечислены `PROJ`, `Epic`, `История`, в описании `attach_files` — `PROJ`, каталог `out` и расширения, в описании `add_comment` — `PROJ`, в описании `link_issues` — фразы `blocks`, `is blocked by`, `relates to`, в описании `update_issue` — строка `- PROJ: epic_link (issue_key)`. `sleep` держит stdin открытым: на конце ввода MCP SDK закрывает соединение, и ответы на ещё не обработанные запросы теряются.

## 6. Подключить к Claude Code

Сервер не запускается вручную и не работает в фоне: Claude Code сам запускает `mcp-jira serve` при старте сессии и завершает его при выходе.

Выбрать 6.1 или 6.2.

### 6.1. Для всех, кто работает с проектом: `.mcp.json`

Подходит для всех режимов; для `env` — единственный вариант. Добавить запись в `mcpServers` файла `.mcp.json` в корне проекта:

```json
"scoped-jira": {
  "type": "stdio",
  "command": "mcp-jira",
  "args": ["serve"],
  "env": {"MCP_JIRA_TOKEN": "${MCP_JIRA_TOKEN:-}"}
}
```

Пустое значение по умолчанию в `${MCP_JIRA_TOKEN:-}` обязательно. Без него при незаданной переменной Claude Code передаёт серверу буквальный текст `${MCP_JIRA_TOKEN}`, сервер принимает его за токен, и Jira отвечает 401. С пустым значением сервер не стартует и печатает `mcp-jira: config: token is not available: secret: not found: MCP_JIRA_TOKEN is not set`.

В режимах `keychain` и `file` сервер переменную не читает, поле `env` можно убрать.

### 6.2. Только для себя: `claude mcp add`

Запись попадает в `~/.claude.json`, репозиторий не меняется (`<путь к проекту>` — подставить свой):

```bash
cd <путь к проекту>
claude mcp add scoped-jira -- mcp-jira serve
```

Только для режимов `keychain` и `file`: в этой записи нет поля `env`. Для режима `env` — вариант 6.1.

### 6.3. Запуск (для любого варианта)

Перед запуском, по режиму:

- **keychain:** после перезагрузки сначала разблокировать связку (раздел «После перезагрузки»);
- **env:** запускать из терминала, где загружен `~/.zshrc`.

Для всех режимов — запустить Claude Code из каталога проекта:

```bash
cd <путь к проекту> && claude
```

При подключении через `.mcp.json` (6.1) Claude Code спросит, доверять ли серверу `scoped-jira` из этого файла, — разрешить. С вариантом 6.2 вопроса нет. В `/mcp` у `scoped-jira` статус connected.

## 7. Проверить в сессии

1. **Инструмент работает.** Попросить: «создай в PROJ Epic "Проверка mcp-jira" с приоритетом Medium и epic name "Проверка"». В ответе — `key` и `url`; задача открывается по ссылке, исполнитель — вы. Так же проверить «История» с `epic_link` на созданный Epic. Лишние задачи закрыть в Jira руками.
2. **Отказы.** Попросить создать задачу в другом проекте (например, `OTHER`) и задачу типа `Bug` в PROJ. Модель либо вызывает инструмент и получает ошибку, либо отказывается сама, увидев, что проекта или типа нет в списке инструмента, — оба исхода правильные. В Jira ничего не создано.
3. **Вложения.** Положить в `~/agent-out` небольшой `.md` и попросить приложить его к задаче из пункта 1. В ответе — `attached` с `id`; файл виден во вложениях задачи. Затем приложить файл с кириллицей в имени (например, `~/agent-out/проверка-вложения.md`) и открыть задачу в Jira: имя вложения должно совпадать с именем файла, без `?` и «кракозябр». Затем попросить приложить файл не из `out` (например, `~/.zshrc`) — ошибка `outside the directories allowed by attachments.dirs`, в Jira ничего не приложено.
4. **Комментарии.** Попросить: «добавь к задаче из пункта 1 комментарий "h3. Проверка\n*mcp-jira* работает"». В ответе — `id` и `url`; ссылка открывает задачу на этом комментарии, заголовок и жирный текст отрисованы. Затем попросить прокомментировать задачу другого проекта (например, `OTHER-1`) — ошибка `issue key must look like PROJ-123 and belong to an allowed project`, в Jira ничего не добавлено.
5. **Связи.** Попросить: «сделай так, чтобы Epic из пункта 1 блокировал Историю из пункта 1». В ответе — `url` Epic'а; в Jira в блоке Issue Links у Epic'а видно `blocks <История>`, у Истории — `is blocked by <Epic>`. Если стороны перепутаны, значит, отображение сторон связи в сервере неверно — сообщить владельцу mcp-jira. Затем попросить связать Epic с задачей другого проекта (например, `OTHER-1`) — ошибка `issue key must look like PROJ-123 and belong to an allowed project`, в Jira ничего не связано. Лишние связи удалить в Jira руками.
6. **Эпики.** Создать второй Epic (как в пункте 1) и попросить: «перенеси PROJ-… (История из пункта 1) в эпик PROJ-… (второй Epic)». В ответе — `key` и `url` Истории; в Jira у неё в поле Epic Link — второй Epic. Затем попросить: «убери PROJ-… из эпика» — модель передаёт `epic_link: null`, в Jira поле Epic Link пустое. Затем попросить перенести задачу в эпик другого проекта (например, `OTHER-1`) — ошибка `issue key must look like PROJ-123 and belong to an allowed project`, а изменить у задачи другое поле (например, приоритет) — модель откажется сама или получит ошибку `unexpected additional properties`; в Jira ничего не изменено.

## 8. Как просить Claude создать задачу

Обычным текстом: «создай Epic в PROJ про …», «заведи История в PROJ под эпиком PROJ-7630 …». Обязательные поля (у Epic — `priority` и `epic_name`, у «История» — `priority`) модель видит в описании инструмента; если их нет в просьбе, она переспросит или получит ошибку `required field is empty`.

Сервер только создаёт задачи, прикладывает к ним файлы, добавляет комментарии, связывает задачи отношениями из конфига и меняет поля из `update_fields`: прочитать задачу, изменить у неё поля вне `update_fields`, изменить или удалить связь или комментарий он не может — для чтения есть MCP `jira-readonly`. Сервер не повторяет запрос сам: если ответ говорит, что задача могла быть создана, сначала найти её в Jira.

## 9. Как просить Claude приложить файл

Обычным текстом, с путём к файлу и ключом задачи: «приложи к PROJ-7790 `~/agent-out/report.md` и `~/agent-out/scheme.drawio`», «создай Epic про … и приложи к нему `~/agent-out/analysis/summary.md`». Файлы должны лежать в `~/agent-out`; задача — любая задача PROJ, в том числе созданная раньше и чужая. Модель передаёт пути абсолютными или от `~/`.

Сначала проверяются все пути, и если хоть один не подходит, не прикладывается ничего. Файлы загружаются по одному; если какой-то не загрузился, ошибка перечисляет уже приложенные. Jira не отсеивает повторы: повторная просьба приложить уже приложенный файл даст второе вложение с тем же именем — повторять только неприложенные.

Ограничение: сервер проверяет ключ задачи, а не её текущий проект. Задача, перенесённая из PROJ в другой проект, доступна по старому ключу PROJ, и Jira приложит файл к ней.

## 10. Как просить Claude добавить комментарий

Обычным текстом, с ключом задачи: «добавь к PROJ-7790 комментарий: …», «отпишись в PROJ-7630, что прогрев выкачен». Задача — любая задача PROJ, в том числе чужая. Текст — в Jira wiki markup (`h3. Заголовок`, `*жирный*`, `{code}…{code}`, `[текст|url]`), а не в Markdown; модель видит это в описании инструмента. Комментарий виден всем, кто видит задачу: ограничить видимость нельзя.

Jira не отсеивает повторы: повторная просьба даст второй такой же комментарий. Если ответ говорит, что комментарий мог быть добавлен, сначала открыть задачу.

## 11. Как просить Claude связать задачи

Обычным текстом, с двумя ключами и отношением: «PROJ-7790 блокирует PROJ-7791», «свяжи PROJ-7630 с PROJ-7790 (relates to)». Модель выбирает фразу из списка в описании инструмента (`blocks`, `is blocked by`, `relates to` при конфиге из шага 2) и передаёт в `issue_key` задачу, с которой начинается фраза, в `target_key` — вторую: «PROJ-7790 блокирует PROJ-7791» → `issue_key=PROJ-7790`, `relation=blocks`, `target_key=PROJ-7791`. Называйте направление явно: «A блокирует B» или «A заблокирована B». Обе задачи — любые задачи PROJ, в том числе чужие.

Удалить или изменить связь сервер не может — это делается в Jira руками. Если ответ говорит, что связь могла быть создана, сначала открыть задачу и проверить блок Issue Links.

## 12. Как просить Claude перенести задачу в другой эпик

Обычным текстом, с ключом задачи и ключом эпика: «перенеси PROJ-7790 в эпик PROJ-7630», «положи PROJ-7790 под эпик PROJ-7630»; убрать из эпика — «убери PROJ-7790 из эпика». Модель передаёт `issue_key` и `epic_link` — ключ эпика или `null`. Задача — любая задача PROJ, в том числе чужая; эпик — задача PROJ. Остальные поля задачи не меняются.

Изменение идемпотентно: если ответ говорит, что Jira не подтвердила изменение, просьбу можно просто повторить.

## Изменить проекты, типы, поля, вложения или отношения

Конфиг и токен читаются при старте сервера, на ходу не перечитываются. После правки `~/.config/mcp-jira/config.yaml` (шаг 2) проверить его командой `mcp-jira serve </dev/null && echo OK` (шаг 5), затем в Claude Code: `/mcp` → `scoped-jira` → Reconnect. Перезапускать Claude Code не нужно.

## После перезагрузки (только keychain)

Предпочтительно — один раз в своём терминале, до запуска Claude Code:

```bash
security unlock-keychain ~/Library/Keychains/scoped-mcp.keychain-db
```

Иначе при подключении MCP появится диалог разблокировки связки. Вводить пароль в него можно, только если подключение вызвали вы сами (запуск Claude Code, Reconnect в `/mcp`). Диалог пароля связки, который вы не вызывали, — «Отменить»: такое окно может показать любой процесс, чтобы выманить пароль.

Без GUI-сессии (ssh) запуск с заблокированной связкой завершается ошибкой `keychain: user interaction is not allowed (no GUI session or keychain is locked)` или `keychain: access denied` — разблокировать связку в GUI-сессии.

## Обновление mcp-jira

1. `git pull` в scoped-mcp; прочитать изменения в `mcp-jira` и `pkg/secret` от коммита, записанного в шаге 1, — собирать только из просмотренного коммита.
2. `make install-mcp-jira` (шаг 1) — только из чистого дерева scoped-mcp.
3. **keychain:** при первом обращении к токену пересобранный бинарь получит диалог доступа к элементу связки — «mcp-jira хочет использовать… введите пароль связки scoped-mcp». Ввести пароль связки и нажать «Разрешать всегда». Диалог в любой другой момент, не сразу после своего `make install-mcp-jira`, — «Отказать» и переустановить из просмотренного коммита (`git log -1`, `make install-mcp-jira`).
4. `/mcp` → `scoped-jira` → Reconnect.

## Переход с env или token_path на keychain

До перехода токен брался из `MCP_JIRA_TOKEN` или из файла `token_path` (обычно `~/.config/mcp-jira/token`). После обновления mcp-jira конфиг с `token_path` не загружается (`config: malformed YAML`), а без ключа `secrets` действует режим `env`.

1. `make install-mcp-jira` из просмотренного коммита (шаг 1).
2. Убрать из конфига `token_path` и прежний `secrets`, если он есть, и дописать режим:
   ```bash
   sed -i '' -e '/^token_path:/d' -e '/^secrets:/d' ~/.config/mcp-jira/config.yaml
   printf '\nsecrets: keychain\n' >> ~/.config/mcp-jira/config.yaml
   ```
3. Сохранить действующий токен в связку (шаг 4):
   - токен в файле `token_path` — подать файл на вход, токен на экран не выводится (путь — свой `token_path`):
     ```bash
     mcp-jira token < ~/.config/mcp-jira/token
     ```
   - токен в переменной `MCP_JIRA_TOKEN` — подать её на вход в терминале, где загружен `~/.zshrc`; токен на экран не выводится:
     ```bash
     printenv MCP_JIRA_TOKEN | mcp-jira token
     ```
4. Проверить: `mcp-jira serve </dev/null && echo OK` (шаг 5). Если ошибка — разобраться по таблице «Если что-то не работает» и дальше не идти: старый токен ещё нужен.
5. Только после успешной проверки удалить старые копии токена: файл `rm ~/.config/mcp-jira/token` и строку `export MCP_JIRA_TOKEN=…` из `~/.zshrc`, если они есть. Токен лежал открытым текстом и мог быть прочитан агентом, поэтому по желанию перевыпустить его (шаг 3, «Ротация токена»).
6. Поле `env` у `scoped-jira` в `.mcp.json` можно оставить: в режиме `keychain` переменная не читается.
7. `/mcp` → `scoped-jira` → Reconnect. Если переменная была в окружении Claude Code — выйти и запустить его заново из терминала без неё.

Переход на `file` — так же, с `secrets: file` в шаге 2; токен ляжет в `~/.config/mcp-jira/secrets/token`.

## Ограничение: агент может прочитать токен

Гарантии сервера относятся к тому, что делается через MCP. Токен даёт доступ к Jira от вашего имени с вашими правами. В режимах `env` и `file` агент с доступом к Bash может прочитать его (переменную окружения или файл) и обратиться к Jira в обход MCP — отдельной защиты от этого в этих режимах нет. В режиме `keychain` без пароля связки токен читает только установленный бинарь `mcp-jira` (см. [README, «Хранение токена»](README.md#хранение-токена)).

Почему в режимах `env` и `file` не закрыто другими средствами:

- deny-правила Claude Code: чтение через интерпретатор (`python -c`, `sh -c`) они не ловят, а значит не дают гарантии;
- sandbox Bash: он меняет работу всего Bash агента (`ssh` и `git push` по SSH через него не работают).

Варианты усиления на будущее: sandbox с `filesystem.denyRead`, технический аккаунт с правами только на создание.

## Ротация токена

За несколько дней до истечения срока (Jira присылает письмо):

1. В Jira выпустить новый токен (шаг 3). Старый пока не отзывать.
2. Переключить сервер на новый токен:
   - **keychain, file:** `mcp-jira token` (перезаписывает), затем в Claude Code `/mcp` → `scoped-jira` → Reconnect; сервер перезапустится и прочитает новый токен. Перезапускать Claude Code не нужно.
   - **env:** блок шага 4 не запускать — существующую строку он не меняет. Поправить `export MCP_JIRA_TOKEN=` в `~/.zshrc` руками, выполнить `source ~/.zshrc`, выйти из Claude Code и запустить его заново из этого терминала (Reconnect окружение Claude Code не обновляет).
3. В Jira: **Profile** → **Personal Access Tokens** → **Revoke** у старого токена.

При подозрении на утечку порядок другой: сразу отозвать старый токен (пункт 3 выше), потом выпустить новый и переключиться.

## Если что-то не работает

Ошибки запуска печатаются в stderr с префиксом `mcp-jira:`, ошибки инструмента модель получает ответом.

| Симптом | Что делать |
|---|---|
| `mcp-jira: command not found` | Шаг 1. |
| `mcp-jira: usage: mcp-jira [--config path] serve\|token` или `unknown subcommand` | Неверные аргументы: подкоманда одна, `serve` или `token`; флаг `--config` пишется до неё. |
| `scoped-jira` в `/mcp` — failed | `mcp-jira serve </dev/null` в терминале покажет ошибку конфига или токена. |
| `read config: open …: no such file or directory` | Конфига нет: шаг 2, или путь в `--config` неверен. |
| `config: malformed YAML` / `config: …` с описанием правила | Ошибка в конфиге: правила валидации — в README; сверить с шагом 2. Частые случаи: `base_url` с `user@` в адресе, оставшийся ключ `token_path` (раздел «Переход с env или token_path на keychain»), поле `assignee` в `fields` (исполнителя ставит `assign_to_self`), поле с ключом `issue_key` в `fields` (`config: field key is reserved`: это параметр `update_issue`), ключ в `update_fields`, которого нет в `fields`. |
| `config: tools must enable at least one tool` | В конфиге нет раздела `tools` (конфиг создан до его появления) или все флаги выключены: добавить `tools: {create_issue: true, attach_files: true}`, если в конфиге есть раздел `attachments`, иначе `tools: {create_issue: true}` — прежние инструменты; комментарии и связи включаются отдельно, `add_comment: true` и `link_issues: true` с разделом `issue_links`, изменение полей — `update_issue: true` и `update_fields` у проекта (шаг 2). |
| `config: tools.attach_files and the attachments section must be set together` | Есть `attach_files: true`, но нет раздела `attachments`, или наоборот: добавить недостающее или убрать оба (шаг 2). |
| `config: tools.link_issues and a non-empty issue_links section must be set together` | Есть `link_issues: true`, но раздел `issue_links` пуст или отсутствует, или наоборот: добавить недостающее или убрать оба (шаг 2). |
| `config: issue_links relation must be non-empty, without surrounding spaces and unique` | Фраза `relation` пустая, с пробелами по краям или повторяется: поправить `issue_links` (шаг 2). |
| `config: issue_links type must be non-empty` | У строки `issue_links` нет `type`: указать имя типа связи Jira (шаг 2). |
| `config: issue_links direction must be outward or inward` | `direction` не `outward` и не `inward`: поправить `issue_links` (шаг 2). |
| `config: tools.update_issue and a non-empty update_fields in at least one project must be set together` | Есть `update_issue: true`, но ни у одного проекта нет непустого `update_fields`, или наоборот: добавить недостающее или убрать оба (шаг 2). |
| `config: fields are used only by tools.create_issue and tools.update_issue` | `create_issue` и `update_issue` выключены, а в конфиге остался раздел `fields`: включить нужный инструмент или удалить `fields`. |
| `config: issue_types and assign_to_self are used only by tools.create_issue` | `create_issue` выключен, а у проектов остались `issue_types` или `assign_to_self`: включить `create_issue` или удалить их. |
| `config: attachments.dirs must be non-empty, each absolute or starting with ~/, none giving access to the home directory` | Каталог в `attachments.dirs` пустой, относительный, корень, домашний или выше домашнего: указать в конфиге каталог внутри домашнего, например `~/agent-out` (шаг 2). |
| `config: secrets must be env, file or keychain` | Опечатка в `secrets`. Если после текста идёт `keychain: not supported on this platform` — режим `keychain` есть только на macOS, выбрать `file` или `env`. |
| `config: secrets_dir and keychain_path must be absolute or start with ~/` | Относительный или пустой путь, голый `~` или `~user/…` в `secrets_dir` или `keychain_path`: указать абсолютный путь или `~/…`, либо убрать ключ — тогда действует значение по умолчанию. |
| `config: home directory is unknown, ~ cannot be expanded` | Не задан `HOME`: указать в `--config`, `secrets_dir`, `keychain_path` и `attachments.dirs` абсолютные пути. |
| `config: token is not available: …secret: not found…` | **env** (`…MCP_JIRA_TOKEN is not set`): переменная не задана в окружении Claude Code — `source ~/.zshrc`, запустить Claude Code из этого терминала, проверить блок `env` в `.mcp.json` (шаг 6.1). **keychain, file**: токен не сохранён — `mcp-jira token` (шаг 4). |
| `config: token is not available: keychain: no such keychain…` | Связки нет или `keychain_path` другой: `mcp-jira token` создаст её. |
| `config: token is not available: file: secret must not be accessible by group or others…` | `chmod 700 ~/.config/mcp-jira/secrets`, файлу `token` — `chmod 600`. |
| `config: token is not available: token is empty` | Сохранён пустой токен или переменная из одних пробелов: `mcp-jira token` (env — поправить `~/.zshrc`). |
| `keychain: access denied` — после отказа в диалоге разблокировки связки | Reconnect и ввести пароль связки. |
| `keychain: access denied` — после отказа в ожидаемом диалоге доступа сразу после своего `make install-mcp-jira` | Reconnect, ввести пароль связки, «Разрешать всегда». |
| `keychain: access denied` — после диалога, которого вы не ждали | Пароль не вводить, переустановить из просмотренного коммита (раздел «Обновление mcp-jira»). |
| `keychain: access denied` — запуск без GUI-сессии (ssh) с заблокированной связкой | Разблокировать связку в GUI-сессии. |
| `keychain: user interaction is not allowed (no GUI session or keychain is locked)` | Запуск без GUI-сессии (ssh) с заблокированной связкой: разблокировать её в GUI-сессии. |
| `token: secrets mode is env, set MCP_JIRA_TOKEN in the environment instead` | `mcp-jira token` в режиме `env`: токен задаётся переменной, шаг 4 (env). |
| `token: empty input` | Пустой ввод, токен не сохранён: повторить. |
| `read input: context canceled` | Ввод прерван Ctrl+C, токен не сохранён. |
| `token: hiding terminal input is supported only on macOS, pipe the token into stdin instead` | Не macOS: подать токен через канал, например из менеджера секретов: `… \| mcp-jira token`. |
| `рабочее дерево не чистое: install только из закоммиченного состояния` | `make install-mcp-jira` на дереве с незакоммиченными или неотслеживаемыми файлами: `git status` в scoped-mcp, закоммитить или убрать, повторить. |
| `401 Unauthorized: the token has expired or was revoked; … save it with mcp-jira token or set MCP_JIRA_TOKEN` | Ротация токена. Если токен точно действует — в `.mcp.json` вместо `${MCP_JIRA_TOKEN:-}` стоит `${MCP_JIRA_TOKEN}`, и при незаданной переменной сервер получил буквальный текст вместо токена (шаг 6.1); это возможно только в режиме `env`. При связи та же ошибка возможна, если у вас нет права Link Issues в проекте: запросить его в Jira. |
| Переменная обновлена, но сервер работает со старым токеном | env: окружение сервера наследуется от запущенного Claude Code: перезапустить Claude Code из терминала с новым окружением. |
| Диалог доступа к элементу связки не сразу после своего `make install-mcp-jira` | Бинарь пересобран без вашего ведома или подменён: «Отказать», переустановить из просмотренного коммита. |
| `the issue may have been created, check Jira before retrying` | Jira не подтвердила создание: ответ не получен (сеть, таймаут), пришёл неожиданный статус `2xx` или ошибка сервера `5xx` (после текста — статус и сообщение Jira). Найти задачу в Jira по summary, повторять только если её нет. |
| `the issue was created (201) but its key could not be read` | Задача создана: найти её в Jira по summary, не повторять. |
| `could not determine the token owner to assign the issue to; the issue was not created, it is safe to retry` | У проекта `assign_to_self`, а запрос владельца токена (`GET /rest/api/2/myself`) не удался: сеть, `5xx`, неожиданный ответ, тело ответа больше 1 MiB или не читается (после текста — причина). Задача не создана, можно повторить. |
| `validating "arguments"` … `enum: … does not equal any of` | Проекта, типа или фразы связи нет в конфиге: шаг 2, затем «Изменить проекты, типы, поля, вложения или отношения». |
| `issue type is not allowed for this project` | Тип есть в конфиге, но у другого проекта: шаг 2. |
| `field is not allowed by the config: … for issue type …` | Поле не в списке `fields` этого типа: шаг 2. |
| `field is not allowed by the config: …: not in update_fields of project …` | Поле есть в `update_fields` другого проекта, но не проекта задачи: шаг 2. |
| `validating "arguments"` … `unexpected additional properties` | Параметра нет в `fields` конфига (для `update_issue` — ни в одном `update_fields`): шаг 2. |
| `update_issue needs at least one field besides issue_key` | В `update_issue` передан только ключ задачи: назвать, что изменить (например, эпик), или `null`, чтобы очистить поле. |
| `required field is empty` | Не передано обязательное поле (или `summary`, или текст комментария — `body`): дополнить просьбу. |
| `issue key must look like PROJ-123 and belong to an allowed project` | Ключ задачи (`epic_link`; `issue_key` вложения, комментария, связи или изменения; `target_key` связи) не похож на ключ или его проекта нет в конфиге. |
| `jira: 400 Bad Request: …` с текстом про поле | Значение не принято Jira (нет такого приоритета, компонента) или поле отсутствует на экране создания этого типа: поправить ввод или убрать поле из типа в конфиге; сервер значения не проверяет. |
| `jira: 403 Forbidden…` | У вашего пользователя нет права создавать задачи в этом проекте: запросить права в Jira. |
| `jira: 404 Not Found…` или другой статус `3xx`/`4xx` | Проверить `base_url` в конфиге и что проект существует; текст после статуса, если есть, — сообщение Jira. Статусы `5xx` — в строке «the issue may have been created». |
| `attachment path must be a non-empty absolute or ~/ path of an existing file whose name has no backslash and no control characters, listed once` | Путь пустой или относительный. |
| то же | Имя файла содержит `\` или управляющий символ (например, перевод строки): переименовать файл. |
| то же | Файла нет. |
| то же | Один файл указан дважды, в том числе через симлинк. |
| то же, с `home directory is unknown, ~ cannot be expanded` | Путь от `~/`, а `HOME` в окружении сервера не задан: передать абсолютный путь или запустить Claude Code из обычного терминала. |
| то же, с `permission denied` | Файл нельзя прочитать (например, права `000`): `chmod u+r` на файл. Проверяется до первой загрузки; если права сняли во время загрузки — файлы до него уже приложены. |
| `attachment file was replaced after the check and was not sent` | Файл подменили или удалили между проверкой путей и загрузкой: проверить, что лежит по пути, и повторить для него и следующих. |
| `attachment is reached from its attachments.dirs entry through the home directory or the root: the entry gives access to the home directory` | Файл из домашнего каталога, а каталог из `attachments.dirs`, через который он виден, открывает домашний: симлинк на `~` или корень, `/System`, `/System/Volumes` на macOS. Указать в конфиге каталог внутри домашнего (шаг 2) и класть файлы туда. |
| `attachment is outside the directories allowed by attachments.dirs` | Файл (после раскрытия симлинков) не лежит в `~/agent-out`: перенести его туда или добавить каталог в `attachments.dirs` (шаг 2). |
| `attachment is the token file, the keychain file or the config file` | Файл токена режима `file` (`secrets_dir/token`), файл связки (`keychain_path`) и конфиг приложить нельзя ни по какому пути. |
| `attachment is not a regular file` | Указан каталог, канал или устройство. |
| `attachment extension is not allowed by attachments.extensions` | Расширения нет в `attachments.extensions`: переименовать файл или добавить расширение (шаг 2). |
| `attachment is larger than attachments.max_bytes` | Файл больше `max_bytes` — при проверке или вырос перед загрузкой. |
| `tools: attached to …; … was not attached: …` или `no files were attached to … before …` | Загрузка остановилась на названном файле; уже приложенные перечислены с ID, непопробованные — после `not attempted after it`. Повторять только неприложенные: Jira не отсеивает повторы. |
| `… may have been attached, check the issue before retrying it: …` | Jira не подтвердила загрузку названного файла: он мог быть приложен. Проверить вложения задачи; повторять файлы после него, а его — только если его там нет. |
| `the file was sent but Jira did not confirm the attachment` | Ответ не получен (сеть, таймаут 30 с), `5xx` или неожиданный ответ: файл мог быть приложен. Проверить вложения задачи в Jira, прежде чем повторять. |
| `jira: 404 Not Found…` при вложении | Задачи нет или у вас нет прав на неё. |
| `jira: 413 Request Entity Too Large` | Jira не приняла файл по размеру: лимит Jira меньше `max_bytes`. |
| `the comment was sent but Jira did not confirm it; the comment may have been added, check the issue before retrying` | Ответ не получен (сеть, таймаут 30 с), `5xx` или неожиданный ответ: комментарий мог быть добавлен. Открыть задачу в Jira, повторять, только если комментария нет. |
| `jira: 404 Not Found…` при комментарии | Задачи нет или у вас нет прав на неё. |
| `jira: 400 Bad Request…` при комментарии | Jira не приняла текст комментария; сообщение Jira — после статуса. |
| `relation is not allowed by the config` | `relation` нет среди `issue_links[].relation` (сравнение точное, с учётом регистра): выбрать фразу из списка инструмента или дописать её в `issue_links` (шаг 2). |
| `issue_key and target_key must be different issues` | Задачу попросили связать саму с собой: указать две разные задачи. |
| `the link was sent but Jira did not confirm it; the link may have been created, check the issue before retrying` | Ответ не получен (сеть, таймаут 30 с), `5xx` или неожиданный ответ: связь могла быть создана. Открыть задачу в Jira, повторять, только если связи нет. |
| `jira: 400 Bad Request…` при связи | Jira отклонила связь; сообщение Jira — после статуса. |
| `jira: 404 Not Found…` при связи | Связи в Jira выключены, одной из задач нет или у вас нет прав на неё, или нет такого типа связи: сверить `type` в `issue_links` с `jira_get_link_types` в MCP `jira-readonly`. |
| `the update was sent but Jira did not confirm it; the update is idempotent, it is safe to retry` | Ответ не получен (сеть, таймаут 30 с), `5xx` или неожиданный ответ: изменение могло примениться или нет. Повторить просьбу: повтор безопасен. |
| `jira: 404 Not Found…` при изменении | Задачи нет или у вас нет прав на неё. |
| `jira: 400 Bad Request…` при изменении | Jira отклонила значение поля (например, эпик) или поле недоступно для редактирования; сообщение Jira — после статуса. |
| `add_comment`, `attach_files`, `create_issue`, `link_issues` или `update_issue` нет в списке инструментов | Инструмент не включён в `tools` (шаг 2), или сервер не переподключён после правки конфига. |
| `guard: forbidden: …` | Запрос не прошёл guard и не отправлялся. При работе через `create_issue`, `attach_files`, `add_comment`, `link_issues` и `update_issue` этого быть не должно — ошибка в сервере. |
