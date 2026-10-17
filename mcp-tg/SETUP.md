# Настройка mcp-tg: от ключей API до работы в Claude Code

Команды копируются блоками и выполняются по порядку в обычном терминале (не через `!` в Claude Code: там нет терминала для ввода пароля и кода). Руками делается только то, что требует браузера, телефона или системного диалога macOS.

Путь к клону репозитория scoped-mcp, если он другой, поправить в первой строке шага 3.

Секреты (ключи API и сессия Telegram) хранятся в одном из трёх режимов, он задаётся ключом `secrets` в конфиге:

- `keychain` — связка ключей macOS, рекомендуется: ключи и сессию не прочитает ни агент, ни другой процесс без пароля связки;
- `env` — ключи в переменных окружения, сессия в файле; режим по умолчанию;
- `file` — ключи и сессия в файлах каталога; здесь не расписан, см. [README.md](README.md), «Хранение секретов».

Шаг только для одного режима помечен в заголовке: «(только keychain)» или «(только env)». Варианты внутри шага помечены строкой `keychain:` или `env:` перед блоком; блок без метки — для обоих режимов.

## 1. Получить api_id и api_hash

Ключи выдаются на https://my.telegram.org → API development tools. На один номер телефона — одно приложение.

### 1.1. Из РФ: открыть сайт без VPN

Без VPN сайт не открывается (блокировка DNS), а с VPN форма создания приложения отвечает `ERROR`: Telegram сверяет страну IP со страной номера. Рабочий путь — выключить VPN и прокси и прописать адрес сайта в `/etc/hosts`:

```bash
echo '149.154.167.220 my.telegram.org' | sudo tee -a /etc/hosts
sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder
```

Затем полностью закрыть браузер (Cmd+Q) и открыть окно инкогнито без расширений: блокировщики рекламы тоже ломают форму.

### 1.2. Войти и создать приложение (в браузере)

1. https://my.telegram.org, номер `+7…`, код — в приложении Telegram, в чате «Telegram» (не SMS).
2. API development tools → форма:

   | Поле | Значение |
   |---|---|
   | App title | `MyReader` (латиница без пробелов, без слова «Telegram») |
   | Short name | `MyReader` (5–32 символа, латиница и цифры) |
   | URL | пусто |
   | Platform | Desktop |
   | Description | `Personal read-only client for reading my own chats` |

3. Если ответ `ERROR` — обновить страницу: приложение иногда создаётся, несмотря на ошибку.
4. `Too many tries` — лимит на запрос кода: ждать около суток, раньше не пробовать, каждая попытка может сдвинуть разблокировку.

На странице приложения — `App api_id` (число) и `App api_hash` (32 символа).

### 1.3. Убрать строку из /etc/hosts

```bash
sudo sed -i '' '/my\.telegram\.org/d' /etc/hosts
sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder
```

## 2. Сохранить ключи в ~/.zshrc (только env)

В режиме `keychain` этот шаг пропустить: ключи спросит `mcp-tg login` и положит в связку. Переход на `keychain` с уже настроенного `env` — раздел «Переход с env на keychain».

Claude Code передаёт серверу ключи из своего окружения, поэтому они нужны в профиле оболочки. Команда спрашивает оба значения (hash вводится без эха) и дописывает их в `~/.zshrc`, если их там ещё нет:

```bash
if grep -q '^export MCP_TG_API_ID=' ~/.zshrc; then
  echo 'ключи уже в ~/.zshrc'
else
  read -r 'api_id?api_id: '
  read -rs 'api_hash?api_hash: '; echo
  printf 'export MCP_TG_API_ID=%s\nexport MCP_TG_API_HASH=%s\n' "$api_id" "$api_hash" >> ~/.zshrc
  unset api_id api_hash
fi
source ~/.zshrc
```

## 3. Установить mcp-tg

```bash
SCOPED_MCP_REPO=~/Projects/scoped-mcp
(cd "$SCOPED_MCP_REPO" && make install-mcp-tg)
command -v mcp-tg                  # ожидается тот же путь, что печатает следующая строка
echo "$(go env GOPATH)/bin/mcp-tg"
```

`make install-mcp-tg` подписывает бинарь с hardened runtime (зачем — [README.md](README.md), «Подпись бинаря»). Обновлять mcp-tg — по разделу «Обновление mcp-tg» в конце.

Если `command -v` ничего не печатает, каталог `$(go env GOPATH)/bin` не в `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
```

## 4. Создать конфиг

Существующий конфиг не перезаписывается. Если он уже есть в режиме `env`, а нужен `keychain` — раздел «Переход с env на keychain».

keychain:
```bash
mkdir -p -m 700 ~/.config/mcp-tg
[ -e ~/.config/mcp-tg/config.yaml ] || printf 'secrets: keychain\nchats: []\n' > ~/.config/mcp-tg/config.yaml
cat ~/.config/mcp-tg/config.yaml
```

env:
```bash
mkdir -p ~/.config/mcp-tg
chmod 700 ~/.config/mcp-tg
[ -e ~/.config/mcp-tg/config.yaml ] || printf 'secrets: env\nsession_path: ~/.config/mcp-tg/session.json\nchats: []\n' > ~/.config/mcp-tg/config.yaml
cat ~/.config/mcp-tg/config.yaml
```

`chmod 700` отдельной командой — чтобы права исправились и у каталога, созданного раньше: в режиме `env` сервер отказывается читать сессию из каталога, доступного группе или остальным.

## 5. Войти в Telegram

```bash
mcp-tg login
```

`login` спрашивает по порядку:

1. keychain, только если связки ещё нет: системный диалог «Новый пароль связки» (перед ним `login` печатает `Creating keychain …: choose its password in the macOS dialog`) — придумать пароль для `scoped-mcp.keychain-db`, длинный и уникальный (почему — «После перезагрузки»). Он понадобится после каждой перезагрузки и после каждого `make install-mcp-tg`;
2. keychain, только если ключа ещё нет в связке: `api_id`;
3. keychain, только если ключа ещё нет в связке: `api_hash` (вводится без эха);
4. телефон — `+7…`;
5. код — из приложения Telegram;
6. пароль 2FA, если включена двухэтапная проверка (Настройки → Конфиденциальность → Двухэтапная аутентификация). Это облачный пароль, который вы задавали сами; символы при вводе не отображаются.

Проверить результат.

keychain:
```bash
security show-keychain-info ~/Library/Keychains/scoped-mcp.keychain-db   # ожидается no-timeout: связка не блокируется сама, только при перезагрузке
```

env:
```bash
ls -l ~/.config/mcp-tg/session.json   # ожидается -rw-------
```

Сессия даёт полный доступ к аккаунту: файл не копировать и не коммитить, связку не экспортировать. Если завершить эту сессию в Telegram (Настройки → Устройства), сервер перестанет работать до повторного `mcp-tg login`.

## 6. Выбрать чаты

Сервер читает объединение двух списков, их можно сочетать:

- папка Telegram (`folders`, 6.1) — проще всего: чаты добавляются и убираются в приложении Telegram, конфиг больше не правится;
- явный список (`chats`, 6.2) — чаты выбираются по названию из `mcp-tg dialogs` и записываются в конфиг.

Сначала выполнить в терминале блок с хелперами — он нужен обоим способам и действует до закрытия терминала:

```bash
pick_entries() {
  awk -v parts="$1" 'BEGIN{n=split(tolower(parts), part, "|")}
    function wanted(title,  i){for(i=1;i<=n;i++) if(part[i]!="" && index(title, part[i])) return 1; return 0}
    /^  - id:/{if(keep)printf "%s",blk; blk=""; keep=0}
    /^  /{blk=blk $0 "\n"; if($0~/^    title:/ && wanted(tolower(substr($0,12))))keep=1}
    END{if(keep)printf "%s",blk}' "$2"
}
write_section() {
  local key=$1 config=~/.config/mcp-tg/config.yaml entries
  if [[ ! -f $config ]]; then echo "нет $config: сначала шаг 4" >&2; return 1; fi
  entries=$(cat)
  if [[ -z $entries ]]; then echo "ничего не выбрано, раздел $key не изменён" >&2; return 1; fi
  awk -v key="$key" '$0 ~ ("^" key ":") {skip=1; next} /^[^ ]/ {skip=0} !skip' "$config" > "$config.new" &&
    printf '%s:\n%s\n' "$key" "$entries" >> "$config.new" &&
    mv "$config.new" "$config" || { rm -f "$config.new"; return 1; }
}
count_entries() {
  awk '/^[^ ]/{section=$1} /^  - id:/{n[section]++} END{for(s in n)print s, n[s]}' ~/.config/mcp-tg/config.yaml
}
```

`pick_entries` выбирает записи, в `title` которых есть одна из частей, перечисленных через `|`. Часть — обычный текст без учёта регистра: скобки и другие символы пишутся как есть, например `team (prod)`. `write_section` заменяет раздел конфига целиком, другой раздел не трогает. Если ничего не выбрано или конфига нет, он ничего не пишет. `count_entries` печатает число записей в каждом непустом разделе, например `chats: 2` и `folders: 1`.

### 6.1. Папка Telegram: `folders`

1. В приложении Telegram создать папку (Настройки → Папки с чатами → Создать папку), например «Claude», и добавить в неё чаты поимённо кнопкой «Добавить чаты». Типы чатов из настроек папки (Контакты, Не контакты, Группы, Каналы, Боты) и исключения (Без звука, Прочитанные, Архив) сервер игнорирует: читаются только чаты, добавленные поимённо. «Избранное», даже добавленное в папку поимённо, не читается и явным чатом не считается.
2. Выгрузить папки аккаунта:
   ```bash
   mcp-tg folders > /tmp/folders.yaml
   cat /tmp/folders.yaml
   ```
   Внутри записи папки с категориями печатается комментарий `# categories are ignored, …`; он попадёт и в конфиг. Это не ошибка: явно добавленные чаты такой папки читаются.
3. Перечислить в `FOLDERS` части названий папок через `|` и посмотреть, что выбралось:
   ```bash
   FOLDERS='claude'
   pick_entries "$FOLDERS" /tmp/folders.yaml | grep 'title:'
   ```
   Если выбралось лишнее — уточнить `FOLDERS` и повторить.
4. Записать раздел `folders`:
   ```bash
   pick_entries "$FOLDERS" /tmp/folders.yaml | write_section folders
   rm /tmp/folders.yaml
   count_entries   # folders: <число папок>
   ```

`FOLDERS` — полный список. Папка указывается по `id`, переименование папки в Telegram на конфиг не влияет; `title` — подсказка для человека.

### 6.2. Явный список: `chats`

1. Выгрузить диалоги и посмотреть названия:
   ```bash
   mcp-tg dialogs > /tmp/dialogs.yaml
   grep 'title:' /tmp/dialogs.yaml | sed 's/^ *title: //' | sort | less
   ```
2. Перечислить в `CHATS` части названий через `|` и посмотреть, что выбралось:
   ```bash
   CHATS='team oncall|ivan petrov'
   pick_entries "$CHATS" /tmp/dialogs.yaml | grep 'title:'
   ```
   Если выбралось лишнее (одинаковые названия, слишком короткая часть) — уточнить `CHATS` и повторить.
3. Записать раздел `chats` и удалить выгрузку — в ней все ваши диалоги:
   ```bash
   pick_entries "$CHATS" /tmp/dialogs.yaml | write_section chats
   rm /tmp/dialogs.yaml
   count_entries   # chats: <число чатов>
   ```

`CHATS` — полный список.

### Дальше

Выбрав один или оба способа: при первой настройке — шаг 7; если сервер уже подключён к Claude Code — `/mcp` → `scoped-tg` → Reconnect.

### Что важно знать о доступе

- доступ задаётся на весь чат: все топики форума читаются вместе с ним;
- комментарии к постам канала живут в привязанной группе обсуждения: чтобы их читать, группу тоже добавить в папку или в `CHATS`;
- `title` в конфиге — подсказка для человека, живое название берётся из Telegram;
- чат, попавший и в `chats`, и в папку, учитывается один раз;
- папка, в которой нет ни одного явно добавленного чата, и папка с несуществующим `id` — ошибка запуска сервера, а не пустой доступ.

Если конфиг правится руками: `id` чата — положительный ID MTProto, как его печатает `dialogs` (формат Bot API `-100…` отклоняется); `access_hash` обязателен для `user` и `channel`; `type`: `user` — личка, `chat` — обычная группа, `channel` — канал или супергруппа. `id` папки — положительный, как его печатает `folders`. Хотя бы один из разделов `chats` или `folders` должен быть непуст.

## 7. Проверить сервер без Claude Code

```bash
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_chats","arguments":{}}}'
  sleep 15; } | mcp-tg serve
```

Через несколько секунд приходят две строки JSON; во второй (`"id":2`) — выбранные чаты. Ошибка конфига или сессии печатается в stderr сразу. `sleep` держит stdin открытым: без него сервер закрывается раньше, чем ответит.

## 8. Подключить к Claude Code

Сервер не запускается вручную и не работает в фоне: Claude Code сам запускает `mcp-tg serve` при старте сессии и завершает его при выходе.

Если запись уже есть в `.mcp.json` проекта — перейти к «Запустить Claude Code» ниже. Её блок `env` с `MCP_TG_API_ID`/`MCP_TG_API_HASH` в режиме `keychain` не используется; если этих переменных нет в окружении, блок удалить — см. «Переход с env на keychain».

В другом проекте, только для себя (запись в `~/.claude.json`, репозиторий не меняется):

keychain:
```bash
cd /path/to/project
claude mcp add scoped-tg -- mcp-tg serve
```

env:
```bash
cd /path/to/project
claude mcp add scoped-tg -e MCP_TG_API_ID=$MCP_TG_API_ID -e MCP_TG_API_HASH=$MCP_TG_API_HASH -- mcp-tg serve
```

Или для всех, кто работает с проектом, — в `.mcp.json`.

keychain:
```json
"scoped-tg": {
  "type": "stdio",
  "command": "mcp-tg",
  "args": ["serve"]
}
```

env (ключи подставляются из окружения каждого):
```json
"scoped-tg": {
  "type": "stdio",
  "command": "mcp-tg",
  "args": ["serve"],
  "env": {
    "MCP_TG_API_ID": "${MCP_TG_API_ID}",
    "MCP_TG_API_HASH": "${MCP_TG_API_HASH}"
  }
}
```

Запустить Claude Code (env: из терминала, где загружен `~/.zshrc`):

```bash
cd /path/to/project && claude
```

На вопрос о сервере `scoped-tg` из `.mcp.json` — разрешить. В `/mcp` у `scoped-tg` статус connected.

## 9. Работа

Просить Claude обычным текстом, называя чат как в Telegram:

- «какие чаты доступны в telegram» — `list_chats`;
- «прочитай последние 50 сообщений в Team oncall» — `get_messages`;
- «что писали в Team oncall с 10 октября» — `get_messages` с `since`;
- «найди в Team oncall сообщения про откат релиза» — `search_messages`;
- «какие топики в чате X», «прочитай топик Y» — `list_topics`, `get_messages` с `topic_id`.

Сервер только читает: отправить, отредактировать или отметить прочитанным он не может. Файлы и медиа не скачиваются, в сообщении остаётся метка вида `document: report.pdf`.

## Изменить список чатов

Конфиг и состав папок читаются при старте сервера, на ходу не перечитываются. Для 6.1 и 6.2 в терминале должны быть загружены хелперы из шага 6 (в новом терминале выполнить блок заново).

- добавить или убрать чат в папке — в приложении Telegram, конфиг не менять;
- изменить `chats` — повторить 6.2 с новым `CHATS` (полный список);
- изменить `folders` — повторить 6.1 с новым `FOLDERS` (полный список).

После любого изменения в Claude Code: `/mcp` → `scoped-tg` → Reconnect. Перезапускать Claude Code не нужно.

## После перезагрузки (только keychain)

Предпочтительно — один раз в своём терминале, до запуска Claude Code:

```bash
security unlock-keychain ~/Library/Keychains/scoped-mcp.keychain-db
```

Иначе при подключении MCP появится диалог разблокировки связки — «… хочет использовать связку ключей «scoped-mcp»…». Ввести в него пароль можно, только если вы сами только что вызвали подключение (запуск Claude Code, Reconnect в `/mcp`). Диалог пароля связки, который вы не вызывали сами, — «Отменить»: такое же окно может показать любой процесс через `osascript`, чтобы выманить пароль. Если Claude Code отвалился по таймауту подключения — `/mcp` → `scoped-tg` → Reconnect.

Пароль связки — длинный и уникальный, не от других сервисов: файл `scoped-mcp.keychain-db` читается любым процессом того же пользователя, и пароль к его копии подбирается офлайн.

Без GUI-сессии (ssh) диалог показать некому: запуск с заблокированной связкой завершается ошибкой `keychain: user interaction is not allowed (no GUI session or keychain is locked)` или `keychain: access denied`. Разблокировать связку в GUI-сессии.

## Обновление mcp-tg

env: `git pull` и `make install-mcp-tg` в scoped-mcp, затем `/mcp` → `scoped-tg` → Reconnect.

keychain:

1. `git pull` в scoped-mcp и прочитать изменения в `mcp-tg` и `pkg/secret`: собирать только из просмотренного коммита.
2. `make install-mcp-tg` в scoped-mcp. Он собирает только закоммиченное состояние всего репозитория: на рабочем дереве с незакоммиченными или неотслеживаемыми файлами останавливается с `рабочее дерево не чистое: install только из закоммиченного состояния`. Перед сборкой `go mod verify` сверяет загруженные модули с `go.sum`, а `go install` идёт с `GOFLAGS=-mod=readonly` (перекрывает `GOFLAGS` из `go env -w`). Доверенными остаются Go toolchain и окружение терминала, где запускается `make install-mcp-tg`.
3. При следующем запуске сервера появятся три диалога доступа к элементу связки подряд, по одному на элемент: `mcp-tg: api_id`, `mcp-tg: api_hash`, `mcp-tg: session` — «mcp-tg хочет использовать… введите пароль связки scoped-mcp» с кнопками «Разрешать всегда», «Разрешить» и «Отказать»: у нового бинаря новый хеш подписи, и связка его ещё не знает. В каждом ввести пароль связки и нажать «Разрешать всегда». «Разрешить» пускает одно обращение, а сервер на старте читает сессию несколько раз (проверка при запуске и каждое подключение к Telegram) — диалог появился бы снова. Если Claude Code отвалился по таймауту подключения — `/mcp` → `scoped-tg` → Reconnect.

При первой установке этих диалогов нет: элементы создаёт сам установленный бинарь. Диалог доступа к элементу связки в любой другой момент, не сразу после своего `make install-mcp-tg`, — не ваш бинарь: «Отказать», затем переустановить из просмотренного коммита (`git log -1`, `make install-mcp-tg`).

## Переход с env на keychain

1. `make install-mcp-tg` из просмотренного коммита (шаг 3).
2. Дописать в конфиг режим (строку `secrets: env`, если она есть, удалить; `session_path` в режиме `keychain` не используется):
   ```bash
   sed -i '' '/^secrets:/d' ~/.config/mcp-tg/config.yaml
   printf 'secrets: keychain\n' >> ~/.config/mcp-tg/config.yaml
   ```
3. `mcp-tg login` (шаг 5): сессия из файла в связку не переносится, вход заново.
4. Проверить новую сессию: `mcp-tg serve </dev/null` завершается без ошибки в stderr. Если ошибка есть — разобраться по таблице «Если что-то не работает» и дальше не идти: старая сессия ещё нужна.
5. Только после успешной проверки отозвать старую сессию: в Telegram Настройки → Устройства. У старой и новой сессии одно имя приложения; старая — не самая новая из них, завершить её. Затем удалить её файл: `rm ~/.config/mcp-tg/session.json`.
6. Удалить строки `export MCP_TG_API_ID=…` и `export MCP_TG_API_HASH=…` из `~/.zshrc`.
7. Убрать ключи из настроек MCP:
   - `.mcp.json`: у `scoped-tg` удалить блок `env` (или заменить значения на `${MCP_TG_API_ID:-}` и `${MCP_TG_API_HASH:-}`, как у `scoped-jira`): без переменных в окружении ссылки `${MCP_TG_API_ID}` без значения по умолчанию не подставятся;
   - если сервер подключался через `claude mcp add -e MCP_TG_API_ID=…` (шаг 8), значения ключей лежат в `~/.claude.json`: в каталоге того проекта выполнить
     ```bash
     claude mcp remove scoped-tg
     claude mcp add scoped-tg -- mcp-tg serve
     ```
8. `/mcp` → `scoped-tg` → Reconnect.

## Если что-то не работает

| Симптом | Что делать |
|---|---|
| `mcp-tg: command not found` | Шаг 3. |
| `api_id is not available` | Причина — дальше в сообщении. env: `source ~/.zshrc` и запускать Claude Code из этого терминала (шаг 2). keychain/file с `secret: not found`: `mcp-tg login` спросит ключи заново. `access denied`, `no such keychain`, `user interaction is not allowed`, `secret must not be accessible by group or others` — см. строки ниже. |
| `scoped-tg` в `/mcp` — failed | `mcp-tg serve </dev/null` в терминале покажет ошибку: пустые `chats` и `folders`, ошибка в конфиге, нет сессии, папка без чатов или не найдена. |
| `list_chats` отдаёт названия из конфига | Запрос к Telegram не прошёл; остальные инструменты вернут саму ошибку. |
| Сервер завершился с подсказкой запустить `mcp-tg login` | Сессия отозвана в Telegram: шаг 5, затем Reconnect в `/mcp`. |
| `FLOOD_WAIT` | Telegram ограничил частоту запросов; ошибка содержит число секунд ожидания. |
| `chat is not in the allowlist` | Чата нет ни в `chats`, ни в папках: шаг 6. |
| ``session not found, run `mcp-tg login` `` | Сессии нет в выбранном хранилище (например, после смены `secrets`): шаг 5. |
| `secret must not be accessible by group or others` | Каталог или файл секрета доступен группе или остальным, путь и режим — в конце сообщения. env: каталог сессии (каталог `session_path`, обычно `~/.config/mcp-tg`) должен быть `0700` — `chmod 700 ~/.config/mcp-tg`; без этого останавливаются и `serve`, и `login`. file: `chmod 700 ~/.config/mcp-tg/secrets`. Файлам внутри — `chmod 600`. |
| `api_id must be an integer` или `API_ID_INVALID` | `ask api_id: config: api_id must be an integer` от `mcp-tg login` — введено не число, ключ не сохранён: повторить `login`. В остальных случаях сохранён неверный ключ: `api_id` не число, или пара `api_id`/`api_hash` не совпадает с приложением на my.telegram.org. Повторный `mcp-tg login` сохранённый ключ не спрашивает: сначала удалить неверный (при `API_ID_INVALID` не видно, какой из двух, — удалить оба), затем `mcp-tg login`. file: `rm ~/.config/mcp-tg/secrets/api_id` (или `api_hash`; каталог — `secrets_dir`). keychain: `security delete-generic-password -s mcp-tg -a api_id ~/Library/Keychains/scoped-mcp.keychain-db` (или `-a api_hash`); появится диалог доступа к элементу связки — это ожидаемо, раз вы сами удаляете. env: исправить `MCP_TG_API_ID`/`MCP_TG_API_HASH` в `~/.zshrc` (шаг 2), `source ~/.zshrc`. |
| `рабочее дерево не чистое: install только из закоммиченного состояния` | `make install-mcp-tg` на дереве с незакоммиченными или неотслеживаемыми файлами: `git status` в scoped-mcp, закоммитить или убрать изменения и повторить. |
| `stat secret path: secret: not found` | file: каталога `secrets_dir` или файла ключа нет: `mcp-tg login` спросит ключи и создаст их. |
| `keychain: no such keychain` | Связка не создана или `keychain_path` другой: `mcp-tg login`. |
| `keychain: access denied` | Отказ в одном из диалогов связки или заблокированная связка без GUI. Отказали в диалоге разблокировки связки — перезапустить (`/mcp` → `scoped-tg` → Reconnect) и ввести пароль. Отказали в ожидаемом диалоге доступа к элементу связки сразу после своего `make install-mcp-tg` — `/mcp` → `scoped-tg` → Reconnect, ввести пароль связки и нажать «Разрешать всегда». Отказали в диалоге доступа к элементу связки, которого не ждали, — пароль не вводить: переустановить из просмотренного коммита (строка про неожиданный диалог ниже, раздел «Обновление mcp-tg»). Без GUI-сессии (ssh) так же отвечает заблокированная связка: разблокировать её в GUI-сессии. |
| `keychain: user interaction is not allowed` | Запуск без GUI-сессии (ssh) с заблокированной связкой: разблокировать её в GUI-сессии. Та же ситуация может дать и `keychain: access denied`. |
| Диалог доступа к элементу связки не сразу после своего `make install-mcp-tg` | Бинарь пересобран без вашего ведома или подменён: «Отказать», затем переустановить из просмотренного коммита: `git log -1`, `make install-mcp-tg`. |
| `both chats and folders are empty` | Ни одного чата и ни одной папки в конфиге: шаг 6. |
| `folder not found in the account` | Папки с `id`, напечатанным в конце сообщения, в аккаунте нет (удалена или `id` из другого аккаунта): шаг 6.1. |
| `folder has no explicitly added chats` | В папке с `id`, напечатанным в конце сообщения, только категории или она пуста: добавить в папку чаты поимённо в Telegram, затем Reconnect. |
| Чат добавлен в папку, но `list_chats` его не показывает | Состав папок читается при старте: `/mcp` → `scoped-tg` → Reconnect. |
| `ничего не выбрано, раздел … не изменён` | `FOLDERS` или `CHATS` не совпали ни с одним названием: проверить выгрузку и повторить шаг 6.1 или 6.2. |
