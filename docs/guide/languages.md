# Languages

This page shows, for every language and file format docsync reads, which comment a `ds:def` goes in, how to create one with `ds def`, and exactly which lines the def binds. It is for anyone adding defs to a codebase; the directive syntax itself and the verbs that cite a def are in [Directives](directives.md).

Every example is run against the `ds` binary by the guide's test harness, each section in a fresh repository: the file shown is written, the `ds def` commands are run on it, and the output shown, including the file afterwards, is what `ds` printed. The ids `ds def` mints are random, so yours will differ in their last eight characters.

<!-- doctest
git init -q -b main .
ds init
-->

## Summary

| Language or format | Files | Tier | `ds def path#Name` | Comment form |
|---|---|---|---|---|
| Go | `.go` | `go` (syntax) | functions, methods, types, constants, variables, group entries, imports | `//` |
| TypeScript | `.ts` | `typescript` (syntax) | functions, classes, interfaces, enums, type aliases, constants, members by bare name | `//` |
| TSX | `.tsx` | `tsx` (syntax) | as TypeScript | `//` |
| JavaScript | `.js`, `.jsx`, `.mjs` | `javascript` (syntax) | functions, classes, constants | `//` |
| Python | `.py` | `python` (syntax) | functions, classes, methods by bare name; constants by line | `#` |
| SQL | `.sql` | `sql` (syntax) | statements, by the name they create | `--` |
| YAML | `.yaml`, `.yml`, `Taskfile.yml` | `yaml` (structured) | key path, `server.port` | `#` |
| TOML | `.toml` | `toml` (structured) | key path, `server.port`; tables by line | `#` |
| HCL, Terraform | `.tf`, `.hcl` | `hcl` (structured) | by line only | `#` or `//` |
| INI | `.ini`, `.cfg` | `config` | `section.key` | `#` or `;` |
| dotenv | `.env`, `*.env`, `.tpl` | `config` | `NAME` | `#` |
| Java properties | `.properties` | `config` | `"server.port"` (quoted) | `#` or `!` |
| Other config | `.conf`, `.editorconfig` | `config` | by line | `#` |
| Rust, Kotlin, Ruby, PHP | `.rs`, `.kt`, `.rb`, `.php` | `code` (heuristic) | `fn`, `struct`, `const`, `fun`, `def`, `function` | `//`; `#` for Ruby |
| C, C++, C#, Java, Swift, Scala, Dart, Zig | `.c`, `.h`, `.cpp`, `.cs`, `.java`, `.swift`, `.scala`, `.dart`, `.zig` | `code` (heuristic) | by line | `//` |
| Shell, Perl, R, Elixir, Nix | `.sh`, `.bash`, `.zsh`, `.pl`, `.r`, `.ex`, `.exs`, `.nix` | `code` (heuristic) | by line | `#` |
| Lua, Haskell | `.lua`, `.hs` | `code` (heuristic) | by line | `--` |
| CSS, SCSS | `.css`, `.scss` | `code` (heuristic) | by line | `/* … */` (SCSS also `//`) |
| Dockerfile, Makefile | `Dockerfile`, `Makefile` | `code` (heuristic) | by line | `#` |
| Markdown | `.md`, `.markdown` | `markdown` (document) | heading text | `<!-- … -->` |
| MDX | `.mdx` | `markdown` (document) | heading text | `{/* … */}` |
| HTML, XML, SVG | `.html`, `.htm`, `.xml`, `.svg` | `markdown` (document) | by line | `<!-- … -->` |
| AsciiDoc | `.adoc`, `.asciidoc` | `document` | heading text | `//` |
| reStructuredText | `.rst` | `document` | heading text | `..` |
| Plain text | `.txt`, `.text` | `text` | by line | a bare line |
| JSON, JSONL, CSV, TSV, `go.sum` | `.json`, `.jsonl`, `.csv`, `.tsv`, … | none | not possible | none: use a remote def |

"Tier" is the name `ds check --explain` prints in its TIER column, and `ds doctor` lists the tiers in the order they are tried:

```
$ ds doctor
…
extractors     ok    sql, python, javascript, tsx, typescript, go, hcl, toml, yaml, markdown, document, config, code, text
…
```

The syntax tiers parse the file with a real grammar, so a def binds exactly one declaration. The structured tiers parse YAML, TOML and HCL and bind a key. The `config` tier reads line-oriented config. The `code` tier is a heuristic for every other language with a comment syntax: it recognises common declaration shapes, matches braces, and otherwise binds up to the next blank line. The `text` tier reads everything else.

## Two ways to create a def

`ds def` writes the directive for you, in the file's own comment syntax, and prints the new id. Use `--dry-run` to see where it would go.

- **By symbol**, `ds def path#Name`: the symbol column above says what can be named. In code, `Name` is a declaration's name or `Type.Method`; in YAML, TOML, INI and dotenv it is a key path; in markdown, AsciiDoc and rst it is a heading's text.
- **By line**, `ds def path:N`: binds whatever starts at line N, exactly as a hand-written directive above that line would. This works in every file that has a comment syntax, and is the way to reach anything the symbol lookup does not find.

When `ds def` is given a line in a file with no symbol, it derives the label from the file name (`dockerfile-…`, `makefile-…`). For a name it cannot turn into a label, such as `.gitignore`, pass `--label`:

```text file=.gitignore
node_modules/
dist/
```

```
$ ds def .gitignore:1
ds: id: prefix must be lowercase words separated by dashes
$ ds def .gitignore:1 --label ignore-node
ignore-node-f7cf2bka
```

Before writing, `ds def` checks the edit would bind cleanly and refuses if it would not, so a refused `ds def` never touches the file.

In code, write a directive by hand on its own line directly above the declaration. A directive at the end of a code line binds that one line only, as kind `line` with no symbol:

```go file=trail/trail.go
package trail

const (
	A = 1 // ds:def id=go-trail-h3j4k5m6
	B = 2
)
```

```
$ ds scan
2 files, 2 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file trail/
go-trail-h3j4k5m6  line  trail/trail.go:4-4    cited by 0
```

Config files are the exception: there the trailing form is the normal one.

In every section below, `ds find --file <dir>` lists what each def bound: its id, its kind, and its file and line range. `ds find <symbol>` finds a def by the symbol it was recorded under.

## Go

<!-- doctest
mkdir ../go
cd ../go
git init -q -b main .
ds init
-->

Comment: `//`. Tier: `go`. Start with:

```go file=go/limits.go
package limits

import (
	"errors"
	str "strings"
)

const (
	MinLength = 8
	MaxLength = 256
)

var ErrTooLong = errors.New("too long")

type Limits struct {
	MinLength int
	MaxLength int
}

type Checker interface {
	Check(s string) error
}

func (l Limits) Check(s string) error {
	if len(s) > l.MaxLength {
		return ErrTooLong
	}
	_ = str.TrimSpace(s)
	return nil
}
```

Define a method, a type, an interface, a constant inside a group, an import, the struct field `Limits.MinLength` (by line, see below), and finally the whole `const ( … )` group (by its line). Each `ds def` inserts a line, so the line numbers count from the bottom up:

```
$ ds def go/limits.go#Limits.Check
limits-check-xx2qcpxu
$ ds def go/limits.go#Checker
checker-mvqbrh58
$ ds def go/limits.go:16
limits-minlength-3ufswfvp
$ ds def go/limits.go#Limits --label limits-type
limits-type-ej8sdmj5
$ ds def go/limits.go#MaxLength
maxlength-s5thyjpj
$ ds def go/limits.go:8
minlength-epm9kzhs
$ ds def go/limits.go#strings
strings-x4ewf8ea
$ cat go/limits.go
package limits

import (
	"errors"
	// ds:def id=strings-x4ewf8ea
	str "strings"
)

// ds:def id=minlength-epm9kzhs
const (
	MinLength = 8
	// ds:def id=maxlength-s5thyjpj
	MaxLength = 256
)

var ErrTooLong = errors.New("too long")

// ds:def id=limits-type-ej8sdmj5
type Limits struct {
	// ds:def id=limits-minlength-3ufswfvp
	MinLength int
	MaxLength int
}

// ds:def id=checker-mvqbrh58
type Checker interface {
	Check(s string) error
}

// ds:def id=limits-check-xx2qcpxu
func (l Limits) Check(s string) error {
	if len(s) > l.MaxLength {
		return ErrTooLong
	}
	_ = str.TrimSpace(s)
	return nil
}
$ ds scan
1 files, 7 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file go/
checker-mvqbrh58           type   go/limits.go:26-28    cited by 0
limits-check-xx2qcpxu      func   go/limits.go:31-37    cited by 0
limits-minlength-3ufswfvp  const  go/limits.go:21-21    cited by 0
limits-type-ej8sdmj5       type   go/limits.go:19-23    cited by 0
maxlength-s5thyjpj         const  go/limits.go:13-13    cited by 0
minlength-epm9kzhs         const  go/limits.go:10-14    cited by 0
strings-x4ewf8ea           const  go/limits.go:6-6      cited by 0
```

The extent rules:

- A function or method binds its whole declaration, signature through closing brace. Methods are recorded as `Type.Method`, and `#Check` finds `Limits.Check` too:

```
$ ds def go/limits.go#Check
limits-check-xx2qcpxu
$ ds find Limits.Check
limits-check-xx2qcpxu  func  go/limits.go:31-37    cited by 0
```

- A type binds its whole declaration.
- Inside `const ( … )`, `var ( … )` or `type ( … )`, a directive above an entry binds that entry only (`MaxLength`, one line). A directive above the group's own `const (` line binds the whole group, recorded under its first entry's name. `ds def path#MaxLength` resolves the entry by name.
- A struct field, an interface method or an embedded type binds itself and is recorded with its holder:

```
$ ds find Limits.MinLength
limits-minlength-3ufswfvp  const  go/limits.go:21-21    cited by 0
```

`ds def path#Limits.MinLength` does not find it, though; use the bare name `#MinLength` when nothing earlier in the file has that name, or `path:N`. Here `#MinLength` would resolve to the constant, so the field was defined by line:

```
$ ds def go/limits.go#Limits.MinLength
ds: extract: symbol not found: declaration "Limits.MinLength"
```

- An import binds its own line and is asked for by its path, `#strings`, or its alias, `#str`; it is recorded under the alias when there is one.
- A one-line value (`MaxLength = 256`) is a fact: `ds:cfg` and `ds facts` show `256`. A string constant shows with its quotes.
- `span=+N` widens a def by N more lines.

`go.mod` and `go.work` take `//` too, and are read line by line by the `text` tier: a def binds the next line.

## TypeScript, TSX and JavaScript

<!-- doctest
mkdir ../ts
cd ../ts
git init -q -b main .
ds init
-->

Comment: `//`. Tiers: `typescript` (`.ts`), `tsx` (`.tsx`), `javascript` (`.js`, `.jsx`, `.mjs`). One set of rules covers all three. Start with:

```typescript file=ts/config.ts
export const DEFAULT_PORT = 5173;

export interface Options {
  port: number;
  host?: string;
}

export enum Mode {
  Dev = "dev",
  Prod = "prod",
}

export class Server {
  maxConnections = 100;

  @logged()
  start(opts: Options): void {
    console.log(opts.port);
  }
}

export function connect(url: string): Promise<void> {
  return fetch(url).then(() => undefined);
}

type Handler = (req: Request) => Response;
```

```
$ ds def ts/config.ts#Handler
handler-8zurw729
$ ds def ts/config.ts#connect
connect-9ksmnfc4
$ ds def ts/config.ts#start
start-r5axv9cy
$ ds def ts/config.ts#Server --label server-class
server-class-u7znm342
$ ds def ts/config.ts#Prod
prod-cwx26tdb
$ ds def ts/config.ts#port
port-6nxhmw97
$ ds def ts/config.ts#DEFAULT_PORT
default-port-tfz6jfe2
$ cat ts/config.ts
// ds:def id=default-port-tfz6jfe2
export const DEFAULT_PORT = 5173;

export interface Options {
  // ds:def id=port-6nxhmw97
  port: number;
  host?: string;
}

export enum Mode {
  Dev = "dev",
  // ds:def id=prod-cwx26tdb
  Prod = "prod",
}

// ds:def id=server-class-u7znm342
export class Server {
  maxConnections = 100;

  @logged()
  // ds:def id=start-r5axv9cy
  start(opts: Options): void {
    console.log(opts.port);
  }
}

// ds:def id=connect-9ksmnfc4
export function connect(url: string): Promise<void> {
  return fetch(url).then(() => undefined);
}

// ds:def id=handler-8zurw729
type Handler = (req: Request) => Response;
$ ds scan
1 files, 7 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file ts/
connect-9ksmnfc4       func   ts/config.ts:28-30    cited by 0
default-port-tfz6jfe2  const  ts/config.ts:2-2      cited by 0
handler-8zurw729       type   ts/config.ts:33-33    cited by 0
port-6nxhmw97          const  ts/config.ts:6-6      cited by 0
prod-cwx26tdb          const  ts/config.ts:13-13    cited by 0
server-class-u7znm342  type   ts/config.ts:17-25    cited by 0
start-r5axv9cy         func   ts/config.ts:22-24    cited by 0
```

- Functions, classes, interfaces, enums, type aliases and `const`/`let`/`var` declarations bind their whole declaration; `export` is part of it.
- Interface properties, enum members, class fields and methods bind themselves and are recorded with their holder (`Options.port`, `Mode.Prod`, `Server.start`). By symbol, ask for the bare name: `#port`, `#Prod`, `#maxConnections`, `#start`. The holder form is not found:

```
$ ds find Server.start
start-r5axv9cy  func  ts/config.ts:22-24    cited by 0
$ ds def ts/config.ts#Options.port
ds: extract: symbol not found: declaration "Options.port"
```

- A decorated method: `ds def path#start` writes the directive between the decorator and the method, and the block is the method without the decorator.
- A property of an object literal binds, named by the keys that lead to it. Create it with `ds def path:N`; `#server.port` is not found by symbol:

```typescript file=ts/vite.config.ts
export default {
  server: {
    port: 5173,
    host: "0.0.0.0",
  },
};
```

```
$ ds def ts/vite.config.ts#server.port
ds: extract: symbol not found: declaration "server.port"
$ ds def ts/vite.config.ts:3
server-port-qup5y6bf
$ ds scan
2 files, 8 defs, 0 refs, 0 problems, 0 skipped
$ ds find server.port
server-port-qup5y6bf  const  ts/vite.config.ts:4-4    cited by 0
$ ds facts
ID                     VALUE                                       WHERE                CITED BY
default-port-tfz6jfe2  5173                                        ts/config.ts:2       0
port-6nxhmw97          port: number;                               ts/config.ts:6       0
prod-cwx26tdb          "prod"                                      ts/config.ts:13      0
handler-8zurw729       type Handler = (req: Request) => Response;  ts/config.ts:33      0
server-port-qup5y6bf   5173                                        ts/vite.config.ts:4  0
```

When the whole object is on one line (`server: { port: 5173, host: "localhost" }`), the def binds that line as `server`.

TSX components are ordinary functions and constants:

```tsx file=web/Button.tsx
type Props = { label: string };

export function Button({ label }: Props) {
  return <button className="btn">{label}</button>;
}

export const Small = () => <Button label="s" />;
```

```
$ ds def web/Button.tsx#Small
small-vxxznftm
$ ds def web/Button.tsx#Button
button-y6hs3qkc
```

In plain JavaScript, `module.exports = { … }` has no name: `ds def path:N` binds it as a statement with an empty symbol, labelled from the file name.

```javascript file=web/util.js
const RETRIES = 3;

function retry(fn) {
  for (let i = 0; i < RETRIES; i++) fn();
}

module.exports = { retry };
```

```
$ ds def web/util.js:7
util-nsbwtyqp
$ ds def web/util.js#retry
retry-54asvg3d
$ ds def web/util.js#RETRIES
retries-rfxv63bx
$ ds scan
4 files, 13 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file web/
button-y6hs3qkc   func   web/Button.tsx:4-6    cited by 0
retries-rfxv63bx  const  web/util.js:2-2       cited by 0
retry-54asvg3d    func   web/util.js:5-7       cited by 0
small-vxxznftm    const  web/Button.tsx:9-9    cited by 0
util-nsbwtyqp     stmt   web/util.js:10-10     cited by 0
```

Caveat: `.mts`, `.cts` and `.cjs` files are not read for directives at the moment, and `ds def` refuses them:

```javascript file=web/legacy.cjs
const A = 1;
```

```
$ ds def web/legacy.cjs:1
ds: docsync: no comment carrier for this file type: .cjs has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=web/legacy.cjs pick=…`), or add the type to [scan] if it does have comments
```

Keep defs in `.ts` and `.js` files, or point at these files with a remote def.

## Python

<!-- doctest
mkdir ../py
cd ../py
git init -q -b main .
ds init
-->

Comment: `#`. Tier: `python`. Start with:

```python file=py/app.py
import os

MAX_RETRIES = 3

class Store:
    """A store."""
    timeout = 30

    @property
    def name(self):
        return "store"

    def save(self, item):
        if not item:
            raise ValueError("empty")
        return True


def connect(url):
    return url
```

Module constants and class attributes are not found by symbol, so they are defined by line:

```
$ ds def py/app.py#MAX_RETRIES
ds: extract: symbol not found: declaration "MAX_RETRIES"
$ ds def py/app.py#Store.save
ds: extract: symbol not found: declaration "Store.save"
$ ds def py/app.py#connect
connect-2fkvtcsc
$ ds def py/app.py#save
save-vbnk5kju
$ ds def py/app.py#name
name-twpzwnrp
$ ds def py/app.py:7
store-timeout-scjg3yuj
$ ds def py/app.py#Store --label store-class
store-class-88b3vcgd
$ ds def py/app.py:3
max-retries-2hgcy9ty
$ cat py/app.py
import os

# ds:def id=max-retries-2hgcy9ty
MAX_RETRIES = 3

# ds:def id=store-class-88b3vcgd
class Store:
    """A store."""
    # ds:def id=store-timeout-scjg3yuj
    timeout = 30

    @property
    # ds:def id=name-twpzwnrp
    def name(self):
        return "store"

    # ds:def id=save-vbnk5kju
    def save(self, item):
        if not item:
            raise ValueError("empty")
        return True


# ds:def id=connect-2fkvtcsc
def connect(url):
    return url
$ ds scan
1 files, 6 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file py/
connect-2fkvtcsc        func   py/app.py:25-26    cited by 0
max-retries-2hgcy9ty    const  py/app.py:4-4      cited by 0
name-twpzwnrp           func   py/app.py:14-15    cited by 0
save-vbnk5kju           func   py/app.py:18-21    cited by 0
store-class-88b3vcgd    type   py/app.py:7-21     cited by 0
store-timeout-scjg3yuj  const  py/app.py:10-10    cited by 0
$ ds find Store.timeout
store-timeout-scjg3yuj  const  py/app.py:10-10    cited by 0
```

- A function, method or class binds its whole indented body. Methods are recorded as `Class.method`, but found by symbol only under their bare name (`#save`, `#name`).
- A module-level assignment and a class attribute bind their line and are facts: `ds facts` shows `3` and `30`.
- With a decorator, `ds def` writes the directive between the decorator and the `def`, which is valid Python; the block is the function without its decorator.

Caveat: `.pyi` stub files are not read for directives at the moment.

## SQL

<!-- doctest
mkdir ../sql
cd ../sql
git init -q -b main .
ds init
-->

Comment: `--` (block comments `/* … */` are accepted too). Tier: `sql`. Start with:

```sql file=sql/schema.sql
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  expires_at TIMESTAMP NOT NULL
);

CREATE INDEX sessions_expires ON sessions (expires_at);

DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

```
$ ds def sql/schema.sql:8
delete-9cm43m82
$ ds def sql/schema.sql#sessions_expires
sessions-expires-tsdj4es3
$ ds def sql/schema.sql#sessions --label sessions-table
sessions-table-mpvdh56u
$ cat sql/schema.sql
-- ds:def id=sessions-table-mpvdh56u
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  expires_at TIMESTAMP NOT NULL
);

-- ds:def id=sessions-expires-tsdj4es3
CREATE INDEX sessions_expires ON sessions (expires_at);

-- ds:def id=delete-9cm43m82
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
$ ds scan
1 files, 3 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file sql/
delete-9cm43m82            stmt  sql/schema.sql:11-11    cited by 0
sessions-expires-tsdj4es3  stmt  sql/schema.sql:8-8      cited by 0
sessions-table-mpvdh56u    stmt  sql/schema.sql:2-5      cited by 0
```

Each def binds one statement, through its semicolon. `ds def path#sessions` and `path#sessions_expires` find the statements that create those names; anything else is reached by line. The recorded symbol is the name the statement acts on, so the index is recorded as `sessions`.

A statement can be made runnable for `ds:run` with `runnable=true`; see [Directives](directives.md#dsrun--run-something-and-record-the-result).

## YAML

<!-- doctest
mkdir ../yaml
cd ../yaml
git init -q -b main .
ds init
-->

Comment: `#`. Tier: `yaml`. `ds def` writes the directive at the end of the key's line. Start with:

```yaml file=cfg/app.yaml
server:
  port: 8081
  hosts:
    - a.example.com
    - b.example.com
auth:
  session_ttl_days: 30
```

```yaml file=Taskfile.yml
version: '3'
tasks:
  wfsys:up:
    desc: start the stack
    cmds:
      - docker compose up -d
```

```
$ ds def cfg/app.yaml#server.port
server-port-8852e4ng
$ ds def cfg/app.yaml#server.hosts
server-hosts-bt945cmt
$ ds def cfg/app.yaml#auth
auth-g22bfxc2
$ ds def 'Taskfile.yml#tasks.wfsys:up'
tasks-wfsys-up-etqjhk22
$ cat cfg/app.yaml Taskfile.yml
server:
  port: 8081   # ds:def id=server-port-8852e4ng
  hosts:   # ds:def id=server-hosts-bt945cmt
    - a.example.com
    - b.example.com
auth:   # ds:def id=auth-g22bfxc2
  session_ttl_days: 30
version: '3'
tasks:
  wfsys:up:   # ds:def id=tasks-wfsys-up-etqjhk22
    desc: start the stack
    cmds:
      - docker compose up -d
$ ds scan
2 files, 4 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file cfg/
auth-g22bfxc2          key  cfg/app.yaml:6-7    cited by 0
server-hosts-bt945cmt  key  cfg/app.yaml:3-5    cited by 0
server-port-8852e4ng   key  cfg/app.yaml:2-2    cited by 0
$ ds find 'tasks.wfsys:up'
tasks-wfsys-up-etqjhk22  key  Taskfile.yml:3-6    cited by 0
```

- A scalar key binds its line, and its value is a fact (`8081`).
- A key holding a map or a list binds the key and everything nested under it.
- The symbol is the full key path. A colon inside a key is part of it (`tasks.wfsys:up` needs no quoting); a key containing a dot is quoted, `tasks."a.b".desc`.
- A directive on its own line directly above a key binds that key too:

```yaml file=cfg/above.yaml
server:
  # ds:def id=yaml-above-j3k4m5n6
  port: 8081
```

```
$ ds scan
3 files, 5 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file cfg/above.yaml
yaml-above-j3k4m5n6  key  cfg/above.yaml:3-3    cited by 0
```

Caveat: `span=` is ignored in YAML; a def binds the key it names and its children.

## TOML

<!-- doctest
mkdir ../toml
cd ../toml
git init -q -b main .
ds init
-->

Comment: `#`. Tier: `toml`. Start with:

```toml file=cfg/app.toml
title = "app"

[server]
port = 8081
host = "0.0.0.0"

[database.pool]
max = 20
```

```
$ ds def cfg/app.toml#server.port
server-port-x236d9ns
$ ds def cfg/app.toml#database.pool.max
database-pool-max-t2ncznbs
$ ds def cfg/app.toml#server
ds: extract: symbol not found: key "server"
$ ds def cfg/app.toml:3 --label server-table
server-table-egp4erfz
$ cat cfg/app.toml
title = "app"

[server]   # ds:def id=server-table-egp4erfz
port = 8081   # ds:def id=server-port-x236d9ns
host = "0.0.0.0"

[database.pool]
max = 20   # ds:def id=database-pool-max-t2ncznbs
$ ds scan
1 files, 3 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file cfg/
database-pool-max-t2ncznbs  key  cfg/app.toml:8-8    cited by 0
server-port-x236d9ns        key  cfg/app.toml:4-4    cited by 0
server-table-egp4erfz       key  cfg/app.toml:3-5    cited by 0
$ ds facts
ID                          VALUE  WHERE           CITED BY
server-port-x236d9ns        8081   cfg/app.toml:4  0
database-pool-max-t2ncznbs  20     cfg/app.toml:8  0
```

- A key binds its line, named by its table path, and its value is a fact.
- A table header binds the table, up to the next table. A table is not found by symbol, so define it with `ds def path:N` on its header line.

Caveat: `span=` is ignored in TOML.

## HCL and Terraform

<!-- doctest
mkdir ../hcl
cd ../hcl
git init -q -b main .
ds init
-->

Comment: `#` (or `//`). Tier: `hcl`. Files: `.tf`, `.hcl`; `.tfvars` is read too, but see the caveat. Start with:

```hcl file=cfg/main.tf
variable "region" {
  default = "us-east-1"
}

resource "aws_instance" "web" {
  instance_type = "t3.micro"
  tags = {
    Name = "web"
  }
}
```

Nothing is found by symbol, so every def is made by line, bottom up, with a label for each:

```
$ ds def cfg/main.tf#resource.aws_instance.web.instance_type
ds: extract: symbol not found: declaration "resource.aws_instance.web.instance_type"
$ ds def cfg/main.tf:8
ds: docsync: the directive would not bind: extract: ds:def has nothing after it to bind to
$ ds def cfg/main.tf:6 --label instance-type
instance-type-7tay2hrx
$ ds def cfg/main.tf:5 --label aws-web
aws-web-zr5s3s55
$ ds def cfg/main.tf:2 --label region-default
region-default-anmttm88
$ ds def cfg/main.tf:1 --label region-var
region-var-e45mfvg9
$ cat cfg/main.tf
# ds:def id=region-var-e45mfvg9
variable "region" {
  # ds:def id=region-default-anmttm88
  default = "us-east-1"
}

# ds:def id=aws-web-zr5s3s55
resource "aws_instance" "web" {
  # ds:def id=instance-type-7tay2hrx
  instance_type = "t3.micro"
  tags = {
    Name = "web"
  }
}
$ ds scan
1 files, 4 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file cfg/
aws-web-zr5s3s55         key  cfg/main.tf:8-14     cited by 0
instance-type-7tay2hrx   key  cfg/main.tf:10-10    cited by 0
region-default-anmttm88  key  cfg/main.tf:4-4      cited by 0
region-var-e45mfvg9      key  cfg/main.tf:2-5      cited by 0
$ ds find resource.aws_instance.web.instance_type
instance-type-7tay2hrx  key  cfg/main.tf:10-10    cited by 0
```

- A block binds through its closing brace, recorded under its type and labels (`variable.region`, `resource.aws_instance.web`).
- An attribute binds its line and is a fact (`us-east-1`, `t3.micro`).
- An attribute inside a nested map (`Name = "web"` inside `tags = { … }`, line 8 above) cannot be bound, and `ds def` refuses it. Bind the map's attribute (`tags`) or the whole block.
- A remote def can pick an attribute out of any HCL file: `pick=hcl:resource.aws_instance.web.instance_type`.

Caveat: `ds def` refuses `.tfvars` files, but a directive you write by hand is read. `span=` is ignored in HCL.

```hcl file=cfg/dev.tfvars
x = 2
```

```hcl file=cfg/prod.tfvars
x = 1 # ds:def id=tfv-x-d3e4f5g6
```

```
$ ds def cfg/dev.tfvars:1
ds: docsync: no comment carrier for this file type: .tfvars has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=cfg/dev.tfvars pick=…`), or add the type to [scan] if it does have comments
$ ds scan
3 files, 5 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file cfg/prod.tfvars
tfv-x-d3e4f5g6  key  cfg/prod.tfvars:1-1    cited by 0
```

## INI, dotenv, properties and other config

<!-- doctest
mkdir ../config
cd ../config
git init -q -b main .
ds init
-->

Tier: `config`. Comment: `#`; INI and `.cfg` also accept `;`, properties also `!`. `ds def` writes the directive at the end of the key's line; a directive on its own line above the key binds it too.

```ini file=misc/app.ini
[server]
port = 8081
host = localhost
```

```bash file=misc/.env
API_HOST=api.example.com
API_PORT=8081
```

```properties file=misc/app.properties
# ds:def id=prop-above-k3m4n5p6
server.port=8081
```

```nginx file=misc/nginx.conf
worker_processes 4;
listen 80;
```

```
$ ds def misc/app.ini#server.port
server-port-9sr7s89q
$ ds def misc/.env#API_PORT
api-port-d2xsr38r
$ ds def 'misc/app.properties#"server.port"'
prop-above-k3m4n5p6
$ ds def misc/nginx.conf#listen
ds: extract: symbol not found: key "listen"
$ ds def misc/nginx.conf:2
nginx-e7e6tkxy
$ cat misc/app.ini misc/.env misc/nginx.conf
[server]
port = 8081   # ds:def id=server-port-9sr7s89q
host = localhost
API_HOST=api.example.com
API_PORT=8081   # ds:def id=api-port-d2xsr38r
worker_processes 4;
listen 80;   # ds:def id=nginx-e7e6tkxy
$ ds facts
ID                    VALUE       WHERE                  CITED BY
api-port-d2xsr38r     8081        misc/.env:2            0
server-port-9sr7s89q  8081        misc/app.ini:2         0
prop-above-k3m4n5p6   8081        misc/app.properties:2  0
nginx-e7e6tkxy        listen 80;  misc/nginx.conf:2      0
```

- A key binds its line and its value is a fact. INI keys are recorded as `section.key` (the bare key also works by symbol, `#port`), dotenv keys as their name, properties keys quoted when they hold a dot (`"server.port"`). A `.conf` file has no keys docsync knows, so a def binds the line and is reached by `path:N`.
- `span=+N` widens a key's def to N more lines:

```ini file=misc/span.ini
[a]
x = 1   # ds:def id=ini-span-m2n3p4q5 span=+1
y = 2
z = 3
```

```
$ ds scan
5 files, 5 defs, 0 refs, 0 problems, 0 skipped
$ ds read ini-span-m2n3p4q5
x = 1   
y = 2
```

Caveat: put the directive on the line above the key, not at the end of it, in any format whose parser does not accept a comment after a value. Java `.properties` files treat everything after the separator as the value, so a trailing `# ds:def id=…` (what `ds def` writes there) becomes part of the value for Java; many INI parsers behave the same. Write those defs by hand on the line above, as in the properties example.

## Other code: Rust, Kotlin, Ruby, PHP, C, Java, shell, Dockerfile, Makefile and more

<!-- doctest
mkdir ../code
cd ../code
git init -q -b main .
ds init
-->

Tier: `code`. This is a heuristic binder for every language with a comment syntax but no grammar in docsync. It recognises common declaration shapes; it does not parse the language.

- **By symbol** it finds declarations that start with a keyword: Rust `fn`, `pub fn`, `struct`, `const`; Kotlin `fun`; Ruby `def`; PHP `function`. It does not find a declaration that starts with a type or modifiers, such as C's `int add(`, Java's `public class App`, C#'s `public int Add(`, Lua's `local function`, a shell `foo() {`, or a Makefile target. Use `ds def path:N` for those.
- **Extent**: a block with braces binds through its closing brace; anything else binds up to the next blank line. `span=+N` overrides that.

Rust, by symbol:

```rust file=code/lib.rs
pub const MAX_CONN: u32 = 64;

pub struct Pool {
    size: u32,
}

impl Pool {
    pub fn new(size: u32) -> Self {
        Pool { size }
    }
}

pub fn connect(url: &str) -> bool {
    !url.is_empty()
}
```

```
$ ds def code/lib.rs#connect
connect-76suf4x5
$ ds def code/lib.rs#new
new-zv9qq7bm
$ ds def code/lib.rs#Pool
pool-7juqmkbh
$ ds def code/lib.rs#MAX_CONN
max-conn-ncgt9z56
$ ds scan
1 files, 4 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file code/lib.rs
connect-76suf4x5   func   code/lib.rs:17-19    cited by 0
max-conn-ncgt9z56  const  code/lib.rs:2-2      cited by 0
new-zv9qq7bm       func   code/lib.rs:11-13    cited by 0
pool-7juqmkbh      type   code/lib.rs:5-7      cited by 0
```

Java, by line. Without `--label` the label would come from the file name (`app-…` for all three), because these defs have no symbol:

```java file=code/App.java
public class App {
    public static final int PORT = 8080;

    public void run() {
        System.out.println("run");
    }
}
```

```
$ ds def code/App.java#App
ds: extract: symbol not found: declaration "App"
$ ds def code/App.java:4 --label app-run
app-run-kqdx5h53
$ ds def code/App.java:2 --label app-port
app-port-9ysqns5j
$ ds def code/App.java:1 --label app-class
app-class-wkk7f6t7
$ cat code/App.java
// ds:def id=app-class-wkk7f6t7
public class App {
    // ds:def id=app-port-9ysqns5j
    public static final int PORT = 8080;

    // ds:def id=app-run-kqdx5h53
    public void run() {
        System.out.println("run");
    }
}
```

Shell, by line:

```bash file=code/deploy.sh
#!/usr/bin/env bash
set -euo pipefail

REGION=us-east-1

deploy() {
  echo "deploying to $REGION"
  kubectl apply -f k8s/
}

deploy
```

```
$ ds def code/deploy.sh:6 --label deploy-func
deploy-func-re47zmq6
$ ds def code/deploy.sh:4 --label deploy-region
deploy-region-kakh3yyr
$ cat code/deploy.sh
#!/usr/bin/env bash
set -euo pipefail

# ds:def id=deploy-region-kakh3yyr
REGION=us-east-1

# ds:def id=deploy-func-re47zmq6
deploy() {
  echo "deploying to $REGION"
  kubectl apply -f k8s/
}

deploy
```

Dockerfile and Makefile, by line. With no braces, a def binds up to the next blank line:

```dockerfile file=code/Dockerfile
FROM golang:1.23 AS build
WORKDIR /src
COPY . .
RUN go build -o /app ./cmd/app

FROM gcr.io/distroless/base
EXPOSE 8080
ENTRYPOINT ["/app"]
```

```makefile file=code/Makefile
PORT ?= 8080

build:
	go build ./...

test:
	go test ./...
```

```
$ ds def code/Dockerfile:7 --label docker-expose
docker-expose-e6mwzcqk
$ ds def code/Dockerfile:1 --label docker-build
docker-build-2c4hwumb
$ ds def code/Makefile:3 --label make-build
make-build-cr6r4dg6
$ ds def code/Makefile:1 --label make-port
make-port-wmsgk88s
$ ds scan
5 files, 13 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file code/
app-class-wkk7f6t7      stmt   code/App.java:2-10      cited by 0
app-port-9ysqns5j       stmt   code/App.java:4-4       cited by 0
app-run-kqdx5h53        stmt   code/App.java:7-9       cited by 0
connect-76suf4x5        func   code/lib.rs:17-19       cited by 0
deploy-func-re47zmq6    stmt   code/deploy.sh:8-11     cited by 0
deploy-region-kakh3yyr  stmt   code/deploy.sh:5-5      cited by 0
docker-build-2c4hwumb   stmt   code/Dockerfile:2-5     cited by 0
docker-expose-e6mwzcqk  stmt   code/Dockerfile:9-10    cited by 0
make-build-cr6r4dg6     stmt   code/Makefile:5-6       cited by 0
make-port-wmsgk88s      stmt   code/Makefile:2-2       cited by 0
max-conn-ncgt9z56       const  code/lib.rs:2-2         cited by 0
new-zv9qq7bm            func   code/lib.rs:11-13       cited by 0
pool-7juqmkbh           type   code/lib.rs:5-7         cited by 0
```

The comment form per extension: `//` for `.rs`, `.c`, `.h`, `.cpp`, `.java`, `.kt`, `.swift`, `.cs`, `.scala`, `.dart`, `.zig`, `.pkl`; `//` or `#` for `.php`; `#` for `.rb`, `.sh`, `.bash`, `.zsh`, `.pl`, `.r`, `.ex`, `.exs`, `.nix`, `Dockerfile`, `Makefile`; `--` for `.lua`, `.hs`; `/* … */` for `.css` (and `//` for `.scss`).

## Markdown and MDX

<!-- doctest
mkdir ../markdown
cd ../markdown
git init -q -b main .
ds init
-->

Comment: `<!-- … -->` in `.md` and `.markdown`; `{/* … */}` in `.mdx`, which `ds def` writes for you. Tier: `markdown`. In both, keep a directive on one line. Start with:

```markdown file=doc/guide.md
# Guide

## Session policy

Sessions live thirty days.

### Rotation

They rotate on refresh.

## Limits

Ten per user.
```

```mdx file=doc/page.mdx
# Page

## Install

Run the installer.
```

```
$ ds def doc/guide.md:13
guide-fuvccgeu
$ ds def 'doc/guide.md#Rotation'
rotation-jv6jd3za
$ ds def 'doc/guide.md#Session policy'
session-policy-3h7xfkqz
$ ds def 'doc/page.mdx#Install'
install-2pbv8932
$ cat doc/guide.md doc/page.mdx
# Guide

<!-- ds:def id=session-policy-3h7xfkqz -->
## Session policy

Sessions live thirty days.

<!-- ds:def id=rotation-jv6jd3za -->
### Rotation

They rotate on refresh.

## Limits

<!-- ds:def id=guide-fuvccgeu -->
Ten per user.
# Page

{/* ds:def id=install-2pbv8932 */}
## Install

Run the installer.
$ ds scan
2 files, 4 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file doc/
guide-fuvccgeu           paragraph  doc/guide.md:16-16    cited by 0
install-2pbv8932         section    doc/page.mdx:4-6      cited by 0
rotation-jv6jd3za        section    doc/guide.md:9-11     cited by 0
session-policy-3h7xfkqz  section    doc/guide.md:4-11     cited by 0
```

- A def above a heading binds the section: the heading and everything up to the next heading of the same or a higher level. `Session policy` includes its `### Rotation` subsection; the directive anchoring the subsection is left out of the section's hash, so adding it does not flag citations of the section.
- A def above a paragraph binds that paragraph, up to the next blank line.
- By symbol, `#Heading text` finds a heading; the match ignores case.
- An inline fact, `[8081](ds:def?id=…)`, binds just its link text; see [Directives](directives.md#facts-inline-defs-in-prose).
- `span=+N` overrides the extent: the line below the directive plus N more.

```markdown file=doc/span.md
# Span

<!-- ds:def id=md-span-x2x2x2x2 span=+1 -->
Line one.
Line two.
Line three.
```

```
$ ds scan
3 files, 5 defs, 0 refs, 0 problems, 0 skipped
$ ds read md-span-x2x2x2x2
Line one.
Line two.
```

## HTML, XML and SVG

<!-- doctest
mkdir ../html
cd ../html
git init -q -b main .
ds init
-->

Comment: `<!-- … -->`. Tier: `markdown`. `ds def path:N` binds the element that starts on line N, through its closing tag. The label comes from the tag name, so define elements by line:

```html file=doc/index.html
<html>
<body>
<section id="pricing">
  <p>Pro costs $10.</p>
</section>
</body>
</html>
```

```
$ ds def doc/index.html:4
p-5bn2feab
$ ds def doc/index.html:3
section-89xgqp67
$ cat doc/index.html
<html>
<body>
<!-- ds:def id=section-89xgqp67 -->
<section id="pricing">
  <!-- ds:def id=p-5bn2feab -->
  <p>Pro costs $10.</p>
</section>
</body>
</html>
$ ds scan
1 files, 2 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file doc/
p-5bn2feab        element  doc/index.html:6-6    cited by 0
section-89xgqp67  element  doc/index.html:4-7    cited by 0
```

`.vue` and `.svelte` files take `<!-- … -->` in markup (and `//` or `/* … */` in script), and are read by the `text` tier: a def binds the following lines up to the next blank line.

## AsciiDoc and reStructuredText

<!-- doctest
mkdir ../document
cd ../document
git init -q -b main .
ds init
-->

Comment: `//` in AsciiDoc, `..` in rst. Tier: `document`. They bind like markdown: a heading binds its section, anything else its paragraph.

```asciidoc file=doc/guide.adoc
= Guide

== Setup

Install it.

== Usage

Use it.
```

```rst file=doc/guide.rst
Guide
=====

Setup
-----

Install it.
```

```
$ ds def 'doc/guide.adoc#Setup' --label setup-adoc
setup-adoc-zsdp9uze
$ ds def 'doc/guide.rst#Setup' --label setup-rst
setup-rst-mh8pnega
$ cat doc/guide.adoc doc/guide.rst
= Guide

// ds:def id=setup-adoc-zsdp9uze
== Setup

Install it.

== Usage

Use it.
Guide
=====

.. ds:def id=setup-rst-mh8pnega
Setup
-----

Install it.
$ ds scan
2 files, 2 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file doc/
setup-adoc-zsdp9uze  section  doc/guide.adoc:4-6    cited by 0
setup-rst-mh8pnega   section  doc/guide.rst:5-8     cited by 0
```

The rst section includes the title, its underline and the body.

## Plain text

<!-- doctest
mkdir ../text
cd ../text
git init -q -b main .
ds init
-->

Files: `.txt`, `.text`, and any file with an extension docsync does not know. Tier: `text`. There is no comment syntax, so the directive is a line of its own, and renderers strip it. A def binds the following lines up to the next blank line; `span=+N` binds exactly N lines. Note the difference from the other tiers, where `span=+N` means the bound line plus N more.

```text file=doc/oncall.txt
Rota

Week 37  alex
Week 38  someone

Escalation goes to the lead.
```

```text file=doc/span.txt
ds:def id=span-two-c2d3e4f5 span=+2
Week 37  alex
Week 38  someone
Week 39  third
```

```
$ ds def doc/oncall.txt:3
oncall-jj3xfq2x
$ cat doc/oncall.txt
Rota

ds:def id=oncall-jj3xfq2x
Week 37  alex
Week 38  someone

Escalation goes to the lead.
$ ds def doc/oncall.txt#Rota
ds: extract: symbol not found: plain text has no symbols; use path:line
$ ds scan
2 files, 2 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file doc/
oncall-jj3xfq2x    span  doc/oncall.txt:4-5    cited by 0
span-two-c2d3e4f5  span  doc/span.txt:2-3      cited by 0
```

`ds def path:N` writes the bare line into `.txt` and `.text` files. For an unknown extension, or an extensionless file such as `NOTES` or `LICENSE`, `ds def` refuses, because it cannot tell whether the format has a syntax a bare line would break. A directive you add by hand to such a file is read by the `text` tier; if the file is a format with real syntax, use a remote def instead.

```text file=doc/NOTES
Notes

ds:def id=notes-y2y2y2y2
First note.
```

```
$ ds def doc/NOTES:1
ds: docsync: no comment carrier for this file type: NOTES has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=doc/NOTES pick=…`), or add the type to [scan] if it does have comments
$ ds scan
3 files, 3 defs, 0 refs, 0 problems, 0 skipped
$ ds find --file doc/NOTES
notes-y2y2y2y2  line  doc/NOTES:4-4    cited by 0
```

## JSON, CSV and other files without comments

<!-- doctest
mkdir ../json
cd ../json
git init -q -b main .
ds init
-->

JSON (`.json`, `.jsonl`, `.ndjson`, `.geojson`, `.webmanifest`), CSV and TSV, and `go.sum` have no comment syntax, so no directive can go inside them. `ds def` refuses:

```json file=doc/app.json
{"server": {"port": 8081}}
```

```csv file=doc/svc.csv
name,port
api,8081
```

```
$ ds def doc/app.json:1
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=doc/app.json pick=…`), or add the type to [scan] if it does have comments
```

Define the value from a file that can hold a comment, usually the doc that mentions it, with `file=` and `pick=`:

```markdown file=doc/remote.md
# Remote

<!-- ds:def id=json-port-p2q3r4s5 file=doc/app.json pick=json:$.server.port -->
<!-- ds:def id=csv-port-t2u3v4w5 file=doc/svc.csv pick=csv:r2c2 -->

Port [8081](ds:cfg?id=json-port-p2q3r4s5), csv [8081](ds:cfg?id=csv-port-t2u3v4w5).
```

```
$ ds facts
ID                  VALUE  WHERE           CITED BY
json-port-p2q3r4s5  8081   doc/app.json:1  1
csv-port-t2u3v4w5   8081   doc/svc.csv:2   1
```

A remote def hashes only the picked value, so other edits to the file do not flag it. The same form binds a value in any file you would rather not add directives to, with `pick=yaml:…`, `toml:…`, `ini:…`, `env:…`, `hcl:…`, `line:N` or `regex:…`; the full list is in [Directives](directives.md#pick--take-one-value-or-range-out-of-a-block).

A bare `ds:def` line found inside a JSON file breaks it, and `ds check` says so:

```json file=doc/bad.json
{
ds:def id=bad-json-a3b4c5d6
"port": 8081
}
```

```
$ ds check
doc/bad.json
  2	error    problem            scan: directive is not inside a comment: doc/bad.json has no comment syntax, so this line breaks the file; ds repair --apply removes it (bind the value with a remote def instead)
      fix: fix the directive at doc/bad.json:2: scan: directive is not inside a comment: doc/bad.json has no comment syntax, so this line breaks the file; ds repair --apply removes it (bind the value with a remote def instead)
  2	info     uncovered          defined but never cited or covered
      fix: bad-json-a3b4c5d6 is defined but nothing cites or covers it; cite it from a page or remove the def
1 error, 1 info, 2 none
$ ds repair
doc/bad.json:2  delete (no comment syntax here)
  - ds:def id=bad-json-a3b4c5d6
1 line(s) would be repaired, 0 need a person (run with --apply to write)
```

`ds repair --apply` makes the fix: the line is deleted from a file with no comment syntax, and commented out in a file that has one.

## Images and other whole files

For a file whose content cannot be picked apart, such as an image, a PDF or a generated file, a remote def with `pick=file` hashes the whole file. Any change to the file flags the sentences that cite it:

```text file=assets/diagram.txt
+-----+    +-----+
| api | -> | db  |
+-----+    +-----+
```

```markdown file=doc/arch.md
# Architecture

<!-- ds:def id=diagram-q2q2q2q2 file=assets/diagram.txt pick=file -->

The [diagram](ds:block?id=diagram-q2q2q2q2) shows the api talking to the db.
```

<!-- doctest
/bin/rm doc/bad.json
ds scan
git add -A
git commit -q -m arch
-->

```
$ echo '| cache |' >> assets/diagram.txt
$ ds check
doc/arch.md
  5	error    unacked            diagram-q2q2q2q2 changed (moved, body) since this sentence was first cited
      still true: ds ack diagram-q2q2q2q2 --doc doc/arch.md --line 5 --note '…'
      otherwise:  edit the sentence at doc/arch.md:5, then ack
1 error, 2 none
```

## Checking what was bound

After adding defs, three commands show what docsync made of them:

- `ds check --explain` lists every directive with the tier that read it, the kind of block, and the carrier:

```
$ ds check --explain
WHERE            TIER      WHAT      CARRIER  ID
doc/arch.md:3    markdown  def file  comment  diagram-q2q2q2q2
doc/remote.md:3  markdown  def key   comment  json-port-p2q3r4s5
doc/remote.md:4  markdown  def key   comment  csv-port-t2u3v4w5
doc/arch.md:5    markdown  ds:block  link     diagram-q2q2q2q2
doc/remote.md:6  markdown  ds:cfg    link     json-port-p2q3r4s5
doc/remote.md:6  markdown  ds:cfg    link     csv-port-t2u3v4w5
doc/arch.md
  5	error    unacked            diagram-q2q2q2q2 changed (moved, body) since this sentence was first cited
      still true: ds ack diagram-q2q2q2q2 --doc doc/arch.md --line 5 --note '…'
      otherwise:  edit the sentence at doc/arch.md:5, then ack
1 error, 2 none
```

- `ds read <id>` prints the block's body, so you can see exactly where it starts and ends.
- `ds find --file <path>` lists each def's kind and line range; `.ds/ledger.tsv`, written by `ds scan`, also records the symbol.

If a def binds more or less than you meant, move the directive, add `span=+N` where the tier honours it, or bind the value with `pick=`.
