# Настройка mcp-gitlab: от токена до работы в Claude Code

Команды копируются блоками и выполняются по порядку в отдельном терминале (zsh), не через `!` в Claude Code: всё, что вводится и выводится через `!`, попадает в контекст агента, а туда не должен попасть токен.

Нужны: Go не ниже версии из `go.mod` в корне scoped-mcp (`go 1.26.0`), Claude Code CLI (`claude`) и клон репозитория scoped-mcp. Путь к клону, если он другой, поправить в первой строке шага 1.

Режим хранения токена задаётся ключом `secrets` в конфиге:

- `keychain` — связка ключей macOS, рекомендуется: без пароля связки токен читает только установленный бинарь `mcp-gitlab`, ни агент, ни другой процесс;
- `file` — файл `~/.config/mcp-gitlab/secrets/token`, недоступный группе и остальным;
- `env` — переменная окружения `MCP_GITLAB_TOKEN`; режим по умолчанию.

В режимах `file` и `env` агент с Bash может прочитать токен (см. «Ограничение: агент может прочитать токен»).

Блок, нужный не во всех режимах, помечен жирной строкой перед ним: **keychain, file:**, **keychain:** или **env:**. Метка действует до следующей метки или до конца шага. Блок без метки — для всех режимов.

До шага 7 ни одна команда не обращается к GitLab.

## 1. Собрать и установить

```bash
SCOPED_MCP_REPO=~/Projects/scoped-mcp
git -C "$SCOPED_MCP_REPO" log -1 --oneline
(cd "$SCOPED_MCP_REPO" && make install-mcp-gitlab)
command -v mcp-gitlab                 # ожидается тот же путь, что печатает следующая строка
echo "$(go env GOPATH)/bin/mcp-gitlab"
```

Строка `git log` — коммит, из которого собран бинарь: записать его, при обновлении («Обновление mcp-gitlab») изменения читаются от него. `make` печатает выполняемые команды с уже подставленным каталогом установки, в том числе `codesign -s - -f -o runtime "<GOPATH>/bin/mcp-gitlab"`; `codesign` при каждой установке печатает `…: replacing existing signature` — это не ошибка: бинарь Go уже подписан линкером, и `-f` заменяет эту подпись. Признак успеха — `make install-mcp-gitlab` завершился с кодом 0, а подпись с hardened runtime видна так:

```bash
codesign -dv "$(go env GOPATH)/bin/mcp-gitlab" 2>&1 | grep flags   # ожидается runtime в flags, вида flags=0x10002(adhoc,runtime)
```

Если `make install-mcp-gitlab` остановился с `рабочее дерево не чистое: install только из закоммиченного состояния` — в scoped-mcp есть незакоммиченные или неотслеживаемые файлы: `git status`, закоммитить или убрать, повторить. Как устроена сборка и зачем подпись — [README, «Хранение токена»](README.md#хранение-токена).

Если `command -v` ничего не печатает, каталог `$(go env GOPATH)/bin` не в `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
```

## 2. Создать конфиг

Команда создаёт конфиг, только если его ещё нет. В строке `secrets:` указать выбранный режим: `keychain`, `file` или `env`:

```bash
mkdir -p ~/.config/mcp-gitlab && chmod 700 ~/.config/mcp-gitlab
[ -e ~/.config/mcp-gitlab/config.yaml ] || cat > ~/.config/mcp-gitlab/config.yaml <<'YAML'
base_url: https://gitlab.example.com
secrets: keychain

tools:
  add_mr_note: true
  add_mr_line_comment: true
  reply_to_discussion: true
  add_draft_note: true
YAML
cat ~/.config/mcp-gitlab/config.yaml
```

Пути хранилищ по умолчанию — `~/.config/mcp-gitlab/secrets` для `file` и `~/Library/Keychains/scoped-mcp.keychain-db` для `keychain`; другой путь задаётся ключом `secrets_dir` или `keychain_path`.

Раздел `tools` включает инструменты: `add_mr_note` — общий комментарий, `add_mr_line_comment` — комментарий к строке дифа, `reply_to_discussion` — ответ в дискуссию, `add_draft_note` — черновик ревью. Хотя бы один должен быть включён; выключенного инструмента нет в списке инструментов агента, и guard не пропускает его запросы. Проекты не перечисляются — доступен любой проект `gitlab.example.com`, на который есть права у токена. Формат и правила валидации — в [README](README.md#конфиг).

## 3. Выпустить персональный токен (PAT)

В GitLab: аватар → **Edit profile** → **Access tokens** → **Add new token**.

| Поле | Значение |
|---|---|
| Token name | `mcp-gitlab` |
| Expiration date | не дальше 90 дней (или короче по политике команды) |
| Scopes | `api` |

**Скоуп `api` даёт полный доступ к GitLab от имени вашего пользователя** — читать и менять любые проекты, MR, задачи, настройки, на которые у вас есть права.

Почему не другой скоуп: у GitLab нет скоупа «только комментарии»; `read_api` не даёт писать, `write_repository` — только git. Поэтому все ограничения держит guard — проверка каждого HTTP-запроса внутри сервера (см. [README, «Гарантии»](README.md#гарантии)), а токен нужно беречь как пароль (см. «Ограничение: агент может прочитать токен»).

GitLab покажет токен только один раз: не закрывая страницу, перейти к шагу 4. Токен действует от вашего имени: комментарии и черновики создаются вашим пользователем.

## 4. Сохранить токен

**keychain, file:**

```bash
mcp-gitlab token
```

Команда печатает `GitLab personal access token:` — вставить токен из шага 3 (ввод без эха), Enter. Ответ — `Token saved to <путь к связке или файлу>`. Повторный запуск перезаписывает токен. Пустой ввод (`token: empty input`) токен не трогает. Ctrl+C отменяет ввод: токен не сохраняется, команда завершается с ошибкой `read input: context canceled`.

**keychain:**

Запускать установленный бинарь (шаг 1), не `./bin/mcp-gitlab`: элемент связки доверяет бинарю, который его создал.

Если связки `~/Library/Keychains/scoped-mcp.keychain-db` ещё нет (её мог создать другой MCP, например `mcp-tg login` или `mcp-confluence token`), до приглашения ввода токена команда печатает `Creating keychain …: choose its password in the macOS dialog` и показывает системный диалог «Новый пароль связки». Придумать длинный уникальный пароль: он понадобится после каждой перезагрузки и после каждого `make install-mcp-gitlab`. Пока этот диалог открыт, Ctrl+C не прерывает `mcp-gitlab token` — команда завершится только после закрытия диалога.

**env:**

Перед запуском учесть:

- токен ляжет в `~/.zshrc` открытым текстом. Этот файл обычно читается всеми (`0644`) и нередко хранится в git вместе с дотфайлами — проверить, что токен не уйдёт в репозиторий;
- Bash агента наследует окружение Claude Code: токен виден через `env`/`echo $MCP_GITLAB_TOKEN` и может попасть в вывод случайно.

Блок дописывает экспорт в `~/.zshrc`, если его там ещё нет:

```bash
if [ -f ~/.zshrc ] && grep -q '^export MCP_GITLAB_TOKEN=' ~/.zshrc; then
  echo 'MCP_GITLAB_TOKEN уже в ~/.zshrc'
else
  read -rs 'token?PAT: '; echo
  printf "export MCP_GITLAB_TOKEN='%s'\n" "$token" >> ~/.zshrc
  unset token
fi
source ~/.zshrc
```

Как сервер получает переменную: Claude Code запускает `mcp-gitlab serve` и передаёт ему переменные из поля `env` записи в `.mcp.json` (шаг 6.1). Значение `${MCP_GITLAB_TOKEN:-}` там подставляется из окружения самого Claude Code — того, что было в терминале при его запуске. Поэтому новое значение переменной сервер увидит только после перезапуска Claude Code из терминала, где оно уже задано; Reconnect в `/mcp` перезапускает сервер, но окружение Claude Code не меняет. Claude Code запускать из терминала, где `~/.zshrc` загружен (шаг 6.3), сервер подключать через `.mcp.json` (шаг 6.1).

## 5. Проверить сервер без Claude Code

Сначала — что конфиг и токен читаются:

```bash
mcp-gitlab serve </dev/null && echo OK
```

Из `/dev/null` сервер сразу получает конец ввода и завершается: если конфиг и токен в порядке, печатается `OK`, иначе — ошибка (см. [«Если что-то не работает»](#если-что-то-не-работает)). Действует ли токен в GitLab, эта команда не проверяет.

Затем — что инструменты видны. Запрос `tools/list` в GitLab не ходит:

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 3; } | mcp-gitlab serve
```

Приходят две строки JSON; во второй (`"id":2`) — инструменты `add_draft_note`, `add_mr_line_comment`, `add_mr_note`, `reply_to_discussion`. `sleep` держит stdin открытым: на конце ввода MCP SDK закрывает соединение, и ответы на ещё не обработанные запросы теряются.

## 6. Подключить к Claude Code

Сервер не запускается вручную и не работает в фоне: Claude Code сам запускает `mcp-gitlab serve` при старте сессии и завершает его при выходе.

Выбрать 6.1 или 6.2.

### 6.1. Для всех, кто работает с проектом: `.mcp.json`

Подходит для всех режимов; для `env` — единственный вариант. Добавить запись в `mcpServers` файла `.mcp.json` в корне проекта:

```json
"scoped-gitlab": {
  "type": "stdio",
  "command": "mcp-gitlab",
  "args": ["serve"],
  "env": {"MCP_GITLAB_TOKEN": "${MCP_GITLAB_TOKEN:-}"}
}
```

Пустое значение по умолчанию в `${MCP_GITLAB_TOKEN:-}` обязательно. Без него при незаданной переменной Claude Code передаёт серверу буквальный текст `${MCP_GITLAB_TOKEN}`, сервер принимает его за токен, и GitLab отвечает 401. С пустым значением сервер не стартует и печатает `mcp-gitlab: config: token is not available: secret: not found: MCP_GITLAB_TOKEN is not set`.

В режимах `keychain` и `file` сервер переменную не читает, поле `env` можно убрать.

### 6.2. Только для себя: `claude mcp add`

Запись попадает в `~/.claude.json`, репозиторий не меняется (`<путь к проекту>` — подставить свой):

```bash
cd <путь к проекту>
claude mcp add scoped-gitlab -- mcp-gitlab serve
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

При подключении через `.mcp.json` (6.1) Claude Code спросит, доверять ли серверу `scoped-gitlab` из этого файла, — разрешить. С вариантом 6.2 вопроса нет. В `/mcp` у `scoped-gitlab` статус connected.

## 7. Проверить в сессии

Нужен MR в статусе Draft (не путать с черновиками ревью — draft notes) в `group/ci-test` с хотя бы одним изменённым файлом, где в одном дифе есть добавленные, удалённые и неизменённые (контекстные) строки. Все проверки — на нём.

1. **Общий комментарий.** Попросить: «добавь к MR !<iid> в group/ci-test комментарий "Проверка mcp-gitlab"». В ответе — `note_id` и `url`; ссылка открывает MR на этом комментарии, автор — вы.
2. **Комментарий к строке.** Попросить прокомментировать конкретную добавленную строку файла (назвать файл и номер строки в новой версии). В ответе — `discussion_id`, `note_id`, `url`; в UI комментарий стоит на этой строке. То же для удалённой строки (номер в старой версии) и для контекстной (оба номера). Контекстная строка только с `new_line` и только с `old_line`: комментарий стоит на нужной строке, без 400.
3. **Многострочный комментарий** — главная проверка алгоритма `line_code`:
   - создать четыре многострочных комментария, концы которых покрывают все виды строк: контекст → добавленная, удалённая → добавленная, добавленная → удалённая, удалённая → контекст;
   - для каждого проверить: (а) в UI диапазон подсвечен ровно на нужных строках; (б) `get_merge_request_discussion` в read-only MCP `gitlab-readonly` возвращает `position.line_range` с теми же `line_code` и `type`, что отправил сервер;
   - расхождение — дефект в коде (`diff.ParseHunks`, `diff.LineCode` или правило `type`), не в документации.
4. **Ответ в дискуссию.** Попросить ответить в дискуссию из пункта 2 (агент берёт `discussion_id` из ответа или из `gitlab-readonly`). В ответе — `note_id`, `url`; ответ виден в треде.
5. **Черновики.** Попросить создать черновик общий, черновик к строке, черновик-ответ в дискуссию и многострочный черновик (например, удалённая → добавленная). В ответе — `draft_note_id` и ссылка на MR; в UI черновики видны только вам в панели ревью.
6. **Отказы.** Попросить добавить комментарий с текстом `/close` (или с путём `/etc/hosts` в начале строки) — ошибка `tools: no line of the text may start with /…`, в GitLab ничего не добавлено. Попросить «опубликовать черновики» или «approve MR» — у агента нет такого инструмента, он должен отказаться; если он попробует обойти это через Bash с токеном — это ограничение, описанное ниже, а не дефект сервера.
7. **Убрать за собой.** Лишние комментарии удалить в GitLab руками, черновики — опубликовать кнопкой **Submit review** или удалить.

## 8. Как просить агента

Обычным текстом, с проектом и номером MR: «добавь к MR !1517 в group/project комментарий …», «прокомментируй строку 42 файла `pkg/x.go` в MR !1517: …», «ответь в дискуссию … что исправлено», «оставь черновики ревью по MR !1517, публиковать буду сам». Текст — в GitLab Markdown. Строка не должна начинаться с `/`: пути и команды — в обратных кавычках; модель видит это правило в описании инструмента.

Сервер только пишет: прочитать MR, диф и дискуссии он не может — для этого есть MCP `gitlab-readonly`. Черновики он не публикует — это делаете вы кнопкой **Submit review**. Сервер не повторяет запрос сам: если ответ говорит, что комментарий мог быть добавлен, сначала открыть MR.

## Изменить набор инструментов

Конфиг и токен читаются при старте сервера, на ходу не перечитываются. После правки `~/.config/mcp-gitlab/config.yaml` (шаг 2) проверить его командой `mcp-gitlab serve </dev/null && echo OK` (шаг 5), затем в Claude Code: `/mcp` → `scoped-gitlab` → Reconnect. Перезапускать Claude Code не нужно.

## После перезагрузки (только keychain)

Предпочтительно — один раз в своём терминале, до запуска Claude Code:

```bash
security unlock-keychain ~/Library/Keychains/scoped-mcp.keychain-db
```

Иначе при подключении MCP появится диалог разблокировки связки. Вводить пароль в него можно, только если подключение вызвали вы сами (запуск Claude Code, Reconnect в `/mcp`). Диалог пароля связки, который вы не вызывали, — «Отменить»: такое окно может показать любой процесс, чтобы выманить пароль.

Без GUI-сессии (ssh) запуск с заблокированной связкой завершается ошибкой `keychain: user interaction is not allowed (no GUI session or keychain is locked)` или `keychain: access denied` — разблокировать связку в GUI-сессии.

## Обновление mcp-gitlab

1. `git pull` в scoped-mcp; прочитать изменения в `mcp-gitlab` и `pkg/secret` от коммита, записанного в шаге 1, — собирать только из просмотренного коммита.
2. `make install-mcp-gitlab` (шаг 1) — только из чистого дерева scoped-mcp.
3. **keychain:** при первом обращении к токену пересобранный бинарь получит диалог доступа к элементу связки — «mcp-gitlab хочет использовать… введите пароль связки scoped-mcp». Ввести пароль связки и нажать «Разрешать всегда». Диалог в любой другой момент, не сразу после своего `make install-mcp-gitlab`, — «Отказать» и переустановить из просмотренного коммита (`git log -1`, `make install-mcp-gitlab`).
4. `/mcp` → `scoped-gitlab` → Reconnect.

## Ограничение: агент может прочитать токен

Гарантии сервера относятся к тому, что делается через MCP. Токен со скоупом `api` даёт полный доступ к GitLab от вашего имени. В режимах `env` и `file` агент с доступом к Bash может прочитать его (переменную окружения или файл) и обратиться к API в обход MCP — отдельной защиты от этого в этих режимах нет. В режиме `keychain` без пароля связки токен читает только установленный бинарь `mcp-gitlab` (см. [README, «Хранение токена»](README.md#хранение-токена)).

Почему в режимах `env` и `file` не закрыто другими средствами:

- deny-правила Claude Code: чтение через интерпретатор (`python -c`, `sh -c`) они не ловят, а значит не дают гарантии;
- sandbox Bash: он меняет работу всего Bash агента (`ssh` и `git push` по SSH через него не работают).

Варианты усиления на будущее: sandbox с `filesystem.denyRead`, технический аккаунт с правами только на комментарии.

## Ротация токена

За несколько дней до истечения срока (GitLab присылает письмо):

1. В GitLab выпустить новый токен (шаг 3). Старый пока не отзывать.
2. Переключить сервер на новый токен:
   - **keychain, file:** `mcp-gitlab token` (перезаписывает), затем в Claude Code `/mcp` → `scoped-gitlab` → Reconnect; сервер перезапустится и прочитает новый токен. Перезапускать Claude Code не нужно.
   - **env:** блок шага 4 не запускать — существующую строку он не меняет. Поправить `export MCP_GITLAB_TOKEN=` в `~/.zshrc` руками, выполнить `source ~/.zshrc`, выйти из Claude Code и запустить его заново из этого терминала (Reconnect окружение Claude Code не обновляет).
3. В GitLab: **Access tokens** → **Revoke** у старого токена.

При подозрении на утечку порядок другой: сразу отозвать старый токен (пункт 3 выше), потом выпустить новый и переключиться.

## Если что-то не работает

Ошибка может состоять из нескольких текстов через `: ` — обёртка первой, причина после неё; искать в таблице по любому из них.

### При запуске

Ошибки запуска печатаются в stderr с префиксом `mcp-gitlab: `. Если `scoped-gitlab` в `/mcp` в статусе failed, выполнить в терминале `mcp-gitlab serve </dev/null` — он покажет одну из ошибок ниже.

| Текст | Что делать |
|---|---|
| `command not found: mcp-gitlab` (сообщение shell) | Шаг 1. |
| `usage: mcp-gitlab [--config path] serve\|token` или `unknown subcommand` | Неверные аргументы: подкоманда одна, `serve` или `token`; флаг `--config` пишется до неё. |
| `read config: open …: no such file or directory` | Конфига нет: шаг 2, или путь в `--config` неверен. |
| `config: malformed YAML` | Ошибка в конфиге или неизвестный ключ (в том числе в `tools`): сверить с шагом 2. |
| `config: base_url must be an absolute https URL without userinfo, path, query and fragment` | `base_url` с `http://`, с путём, с `user@` или query: должно быть `https://gitlab.example.com`. |
| `config: secrets must be env, file or keychain` | Опечатка в `secrets`. Если после текста идёт `keychain: not supported on this platform` — режим `keychain` есть только на macOS, выбрать `file` или `env`. |
| `config: secrets_dir and keychain_path must be absolute or start with ~/` | Относительный или пустой путь, голый `~` или `~user/…` в `secrets_dir` или `keychain_path`: указать абсолютный путь или `~/…`, либо убрать ключ — тогда действует значение по умолчанию. |
| `config: tools must enable at least one tool` | Нет раздела `tools` или все флаги выключены: шаг 2. |
| `config: home directory is unknown, ~ cannot be expanded` | Не задан `HOME`: указать в `--config`, `secrets_dir` и `keychain_path` абсолютные пути. |
| `config: token is not available: …secret: not found…` | **env** (`…MCP_GITLAB_TOKEN is not set`): переменная не задана в окружении Claude Code — `source ~/.zshrc`, запустить Claude Code из этого терминала, проверить блок `env` в `.mcp.json` (шаг 6.1). **keychain, file**: токен не сохранён — `mcp-gitlab token` (шаг 4). |
| `config: token is not available: keychain: no such keychain…` | Связки нет или `keychain_path` другой: `mcp-gitlab token` создаст её. |
| `config: token is not available: file: secret must not be accessible by group or others…` | `chmod 700 ~/.config/mcp-gitlab/secrets`, файлу `token` — `chmod 600`. |
| `config: token is not available: token is empty` | Сохранён пустой токен или переменная из одних пробелов: `mcp-gitlab token` (env — поправить `~/.zshrc`). |
| `keychain: access denied` — после отказа в диалоге разблокировки связки | Reconnect и ввести пароль связки. |
| `keychain: access denied` — после отказа в ожидаемом диалоге доступа сразу после своего `make install-mcp-gitlab` | Reconnect, ввести пароль связки, «Разрешать всегда». |
| `keychain: access denied` — после диалога, которого вы не ждали | Пароль не вводить, переустановить из просмотренного коммита (раздел «Обновление mcp-gitlab»). |
| `keychain: access denied` — запуск без GUI-сессии (ssh) с заблокированной связкой | Разблокировать связку в GUI-сессии. |
| `keychain: user interaction is not allowed (no GUI session or keychain is locked)` | Запуск без GUI-сессии (ssh) с заблокированной связкой: разблокировать её в GUI-сессии. |
| `token: secrets mode is env, set MCP_GITLAB_TOKEN in the environment instead` | `mcp-gitlab token` в режиме `env`: токен задаётся переменной, шаг 4 (env). |
| `token: empty input` | Пустой ввод, токен не сохранён: повторить. |
| `read input: context canceled` | Ввод прерван Ctrl+C, токен не сохранён. |
| `token: hiding terminal input is supported only on macOS, pipe the token into stdin instead` | Не macOS: подать токен через канал, например из менеджера секретов: `… \| mcp-gitlab token`. |
| `рабочее дерево не чистое: install только из закоммиченного состояния` | `make install-mcp-gitlab` на дереве с незакоммиченными или неотслеживаемыми файлами: `git status` в scoped-mcp, закоммитить или убрать, повторить. |

### В ответе инструмента

| Текст | Что делать |
|---|---|
| `gitlab: 401 Unauthorized: the token has expired or was revoked; …` | Ротация токена или токен без скоупа `api`. Если токен точно действует, в режиме `env` — в `.mcp.json` вместо `${MCP_GITLAB_TOKEN:-}` стоит `${MCP_GITLAB_TOKEN}`, и при незаданной переменной сервер получил буквальный текст вместо токена (шаг 6.1). |
| `gitlab: the merge request could not be fetched; nothing was posted, it is safe to retry` | MR не получен: после текста — причина (`404` — нет проекта, MR или прав; `401`; `5xx`, сеть). Ничего не отправлялось, можно повторить. |
| `gitlab: the file diff could not be fetched; nothing was posted, it is safe to retry` | Диф файла не получен: файла нет в MR (проверить `new_path`), GitLab свернул диф (`collapsed`) или он слишком большой (`too_large`), `5xx`, сеть. Ничего не отправлялось. |
| `gitlab: the response exceeds the size limit` | Ответ GitLab больше 1 MiB (страница дифа — больше 32 MiB); смотреть обёртку перед этим текстом. |
| `tools: the merge request has no diff_refs (no commits yet?) …` | У MR нет коммитов — комментарий к строке невозможен; общий комментарий и ответ в дискуссию работают. |
| `tools: the file diff could not be parsed (binary file?) …` | У файла нет текстового дифа (например, бинарный файл): комментировать его общим комментарием (`add_mr_note`) или черновиком без позиции. |
| `diff: the line is not in the diff of this file` / `diff: the first line of the range comes after the last one` | Номера строк многострочного комментария или однострочного с одним номером не найдены в дифе или перепутаны: у добавленной строки только `new_line`, у удалённой только `old_line`, у контекстной — любой из двух или оба; первая строка раньше последней. |
| `gitlab: 400 Bad Request: …` | GitLab отклонил ввод: чаще всего строки нет в дифе или у переименованного файла не передан `old_path`; сообщение GitLab — после статуса. |
| `gitlab: 404 Not Found: …` | Нет проекта, MR, дискуссии или прав на них; проверить `project`, `mr_iid` и `discussion_id`. |
| `gitlab: the comment was sent but GitLab did not confirm it; it may have been added, check the merge request before retrying` | Ответ не получен (сеть, таймаут 30 с), `5xx` или неожиданный ответ: комментарий мог быть добавлен. Открыть MR, повторять, только если его нет. |
| `tools: no line of the text may start with /…` | Строка текста начинается с `/` (команда или путь): убрать или взять в обратные кавычки. |
| `tools: discussion_id must be 40 lowercase hex characters` | Передан не id дискуссии: взять `discussion_id` из ответа `add_mr_line_comment` или из `gitlab-readonly`. |
| `tools: in_reply_to_discussion_id and a line position are mutually exclusive` | У черновика указаны и дискуссия, и позиция: оставить одно. |
| `Post "…": guard: forbidden: …` (или `Get "…": …`) | Запрос не прошёл guard и не отправлялся. При работе через инструменты этого быть не должно — ошибка в сервере. |

### Симптомы без текста ошибки

| Симптом | Что делать |
|---|---|
| Инструмента нет в списке у агента | Не включён в `tools` (шаг 2), или сервер не переподключён после правки конфига. |
| Переменная обновлена, но сервер работает со старым токеном | env: сервер получает переменную из окружения Claude Code (шаг 4, env): перезапустить Claude Code из терминала с новым окружением. |
| Диалог доступа к элементу связки не сразу после своего `make install-mcp-gitlab` | Бинарь пересобран без вашего ведома или подменён: «Отказать», переустановить из просмотренного коммита. |
