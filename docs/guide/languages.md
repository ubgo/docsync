# Languages

This page shows, for every language and file format docsync reads, which comment a `ds:def` goes in, how to create one with `ds def`, and exactly which lines the def binds. It is for anyone adding defs to a codebase; the directive syntax itself and the verbs that cite a def are in [Directives](directives.md).

Every snippet here was produced by running `ds def`, `ds scan` and `ds check --explain` on real files in a throwaway repository; the bound lines and symbols are copied from the resulting `.ds/ledger.tsv`.

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
extractors     ok    sql, python, javascript, tsx, typescript, go, hcl, toml, yaml, markdown, document, config, code, text
```

The syntax tiers parse the file with a real grammar, so a def binds exactly one declaration. The structured tiers parse YAML, TOML and HCL and bind a key. The `config` tier reads line-oriented config. The `code` tier is a heuristic for every other language with a comment syntax: it recognises common declaration shapes, matches braces, and otherwise binds up to the next blank line. The `text` tier reads everything else.

## Two ways to create a def

`ds def` writes the directive for you, in the file's own comment syntax, and prints the new id. Use `--dry-run` to see where it would go.

- **By symbol**, `ds def path#Name`: the symbol column above says what can be named. In code, `Name` is a declaration's name or `Type.Method`; in YAML, TOML, INI and dotenv it is a key path; in markdown, AsciiDoc and rst it is a heading's text.
- **By line**, `ds def path:N`: binds whatever starts at line N, exactly as a hand-written directive above that line would. This works in every file that has a comment syntax, and is the way to reach anything the symbol lookup does not find.

When `ds def` is given a line in a file with no symbol, it derives the label from the file name (`dockerfile-xt9v8d4t`, `makefile-hzgqxfzh`). For a name it cannot turn into a label, such as `.gitignore`, pass `--label`:

```
$ ds def .gitignore:1
ds: id: prefix must be lowercase words separated by dashes
$ ds def .gitignore:1 --label ignore-node
ignore-node-ug2gnscn
```

Before writing, `ds def` checks the edit would bind cleanly and refuses if it would not, so a refused `ds def` never touches the file.

In code, write a directive by hand on its own line directly above the declaration. A directive at the end of a code line binds that one line only, with no symbol:

```go
const (
	A = 1 // ds:def id=go-trail-h3j4k5m6
	B = 2
)
```

is recorded as kind `line`, line 4. Config files are the exception: there the trailing form is the normal one.

## Go

Comment: `//`. Tier: `go`.

```
$ ds def go/limits.go#MaxLength
$ ds def go/limits.go#Limits
$ ds def go/limits.go#Limits.Check
$ ds def go/limits.go#Checker
$ ds def go/limits.go#strings
$ ds def go/limits.go:8
```

```go
package limits

import (
	"errors"
	// ds:def id=strings-xnmyfkeh
	str "strings"
)

// ds:def id=minlength-2kzadda5
const (
	MinLength = 8
	// ds:def id=maxlength-sq3twxqm
	MaxLength = 256
)

var ErrTooLong = errors.New("too long")

// ds:def id=limits-46fqvam5
type Limits struct {
	// ds:def id=limits-maxlength-field-ynkcj53j
	MinLength int
	MaxLength int
}

// ds:def id=checker-wunywpk8
type Checker interface {
	Check(s string) error
}

// ds:def id=limits-check-nthduk6j
func (l Limits) Check(s string) error {
	if len(s) > l.MaxLength {
		return ErrTooLong
	}
	_ = str.TrimSpace(s)
	return nil
}
```

What each binds, from the ledger:

| id | kind | symbol | lines |
|---|---|---|---|
| `strings-xnmyfkeh` | const | `str` | 6 |
| `minlength-2kzadda5` | const | `MinLength` | 10-14 |
| `maxlength-sq3twxqm` | const | `MaxLength` | 13 |
| `limits-46fqvam5` | type | `Limits` | 19-23 |
| `limits-maxlength-field-ynkcj53j` | const | `Limits.MinLength` | 21 |
| `checker-wunywpk8` | type | `Checker` | 26-28 |
| `limits-check-nthduk6j` | func | `Limits.Check` | 31-37 |

The extent rules:

- A function or method binds its whole declaration, signature through closing brace. Methods are named `Type.Method` through the receiver; `#Check` also finds `Limits.Check`.
- A type binds its whole declaration.
- Inside `const ( … )`, `var ( … )` or `type ( … )`, a directive above an entry binds that entry only. A directive above the group's own `const (` line binds the whole group (here lines 10-14, named after its first entry). `ds def path#MaxLength` resolves the entry by name.
- A struct field, an interface method or an embedded type binds itself and is named with its holder, `Limits.MinLength`. `ds def path#Limits.MinLength` does not find it; use the bare name `#MinLength` when nothing earlier in the file has that name, or `path:N`. Here `#MinLength` resolves to the constant on line 10, so the field was defined by line.
- An import binds its own line and is asked for by its path, `#strings`, or its alias, `#str`; the symbol recorded is the alias when there is one.
- A one-line value (`MaxLength = 256`) is a fact: `ds:cfg` and `ds facts` show `256`. A string constant shows with its quotes, `"eighty"`.
- `span=+N` widens a def by N more lines.

`go.mod` and `go.work` take `//` too, and are read line by line by the `text` tier: a def binds the next line.

## TypeScript, TSX and JavaScript

Comment: `//`. Tiers: `typescript` (`.ts`), `tsx` (`.tsx`), `javascript` (`.js`, `.jsx`, `.mjs`). One set of rules covers all three.

```typescript
// ds:def id=default-port-c9zx2mjr
export const DEFAULT_PORT = 5173;

export interface Options {
  // ds:def id=port-qftp4xnu
  port: number;
  host?: string;
}

export enum Mode {
  Dev = "dev",
  // ds:def id=prod-wyre3dc8
  Prod = "prod",
}

// ds:def id=server-ty7z4wku
export class Server {
  maxConnections = 100;

  @logged()
  // ds:def id=server-start-vrzwmmp7
  start(opts: Options): void {
    console.log(opts.port);
  }
}

// ds:def id=connect-n9fa7n7v
export function connect(url: string): Promise<void> {
  return fetch(url).then(() => undefined);
}

// ds:def id=handler-fvv4mvqw
type Handler = (req: Request) => Response;
```

| id | kind | symbol | lines |
|---|---|---|---|
| `default-port-c9zx2mjr` | const | `DEFAULT_PORT` | 2 |
| `port-qftp4xnu` | const | `Options.port` | 6 |
| `prod-wyre3dc8` | const | `Mode.Prod` | 13 |
| `server-ty7z4wku` | type | `Server` | 17-25 |
| `server-start-vrzwmmp7` | func | `Server.start` | 22-24 |
| `connect-n9fa7n7v` | func | `connect` | 28-30 |
| `handler-fvv4mvqw` | type | `Handler` | 38 |

- Functions, classes, interfaces, enums, type aliases and `const`/`let`/`var` declarations bind their whole declaration; `export` is part of it.
- Interface properties, enum members, class fields and methods bind themselves and are named with their holder (`Options.port`, `Mode.Prod`, `Server.start`). By symbol, ask for the bare name: `#port`, `#Prod`, `#maxConnections`, `#start`. `#Server.start` and `#Options.port` are not found.
- A decorated method: `ds def path#start` writes the directive between the decorator and the method, and the block is the method without the decorator.
- A property of an object literal binds, named by the keys that lead to it. In a multi-line object:

```typescript
export default {
  server: {
    // ds:def id=server-port-7cfpyex5
    port: 5173,
    host: "0.0.0.0",
  },
};
```

That def binds `server.port`, line 4, and `ds:cfg` shows `5173`. Create it with `ds def path:N`; `#server.port` is not found by symbol. When the whole object is on one line (`server: { port: 5173, host: "localhost" }`), the def binds that line as `server`.

TSX components are ordinary functions and constants:

```tsx
type Props = { label: string };

// ds:def id=button-5scawm5j
export function Button({ label }: Props) {
  return <button className="btn">{label}</button>;
}

// ds:def id=small-zv7pdgzd
export const Small = () => <Button label="s" />;
```

`Button` binds lines 4-6; `Small` binds line 9.

In plain JavaScript, `module.exports = { … }` has no name: `ds def path:N` binds it as a statement with an empty symbol, labelled from the file name (`util-6srnmjuj`).

Caveat: `.mts`, `.cts` and `.cjs` files are not read for directives at the moment, and `ds def` refuses them with `no comment carrier for this file type`. Keep defs in `.ts` and `.js` files, or point at these files with a remote def.

## Python

Comment: `#`. Tier: `python`.

```python
import os

# ds:def id=max-retries-qrhuvmhg
MAX_RETRIES = 3

# ds:def id=store-gcgn6bhr
class Store:
    """A store."""
    # ds:def id=store-timeout-4sv3fvha
    timeout = 30

    @property
    # ds:def id=name-3b9gxnwy
    def name(self):
        return "store"

    # ds:def id=save-rj2d9gww
    def save(self, item):
        if not item:
            raise ValueError("empty")
        return True


# ds:def id=connect-eevjszgm
def connect(url):
    return url
```

| id | kind | symbol | lines |
|---|---|---|---|
| `max-retries-qrhuvmhg` | const | `MAX_RETRIES` | 4 |
| `store-gcgn6bhr` | type | `Store` | 7-21 |
| `store-timeout-4sv3fvha` | const | `Store.timeout` | 10 |
| `name-3b9gxnwy` | func | `Store.name` | 14-15 |
| `save-rj2d9gww` | func | `Store.save` | 18-21 |
| `connect-eevjszgm` | func | `connect` | 25-26 |

- A function, method or class binds its whole indented body. Methods are named `Class.method`.
- A module-level assignment and a class attribute bind their line and are facts: `ds facts` shows `3` and `30`.
- By symbol, `ds def` finds functions, classes and methods by their bare name (`#save`, `#name`, `#Store`); `#Store.save` is not found. Module constants and class attributes are not found by symbol (`#MAX_RETRIES`, `#timeout`): use `ds def path:N`.
- With a decorator, `ds def` writes the directive between the decorator and the `def`, which is valid Python; the block is the function without its decorator.

Caveat: `.pyi` stub files are not read for directives at the moment.

## SQL

Comment: `--` (block comments `/* … */` are accepted too). Tier: `sql`.

```sql
-- ds:def id=sessions-8pzu5zz7
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  expires_at TIMESTAMP NOT NULL
);

-- ds:def id=sessions-expires-6szx4teg
CREATE INDEX sessions_expires ON sessions (expires_at);

-- ds:def id=delete-u3e84e69
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

Each def binds one statement, through its semicolon: the table is lines 2-5, the index line 8, the delete line 11. `ds def path#sessions` and `path#sessions_expires` find the statements that create those names; anything else is reached by line. The recorded symbol is the name the statement acts on, so the index is recorded as `sessions`.

A statement can be made runnable for `ds:run` with `runnable=true`; see [Directives](directives.md#dsrun--run-something-and-record-the-result).

## YAML

Comment: `#`. Tier: `yaml`. `ds def` writes the directive at the end of the key's line.

```
$ ds def cfg/app.yaml#server.port
$ ds def cfg/app.yaml#server.hosts
$ ds def cfg/app.yaml#auth
$ ds def 'Taskfile.yml#tasks.wfsys:up'
```

```yaml
server:
  port: 8081   # ds:def id=server-port-6btxuz6q
  hosts:   # ds:def id=server-hosts-rkm9nch2
    - a.example.com
    - b.example.com
auth:   # ds:def id=auth-kkwgh3ax
  session_ttl_days: 30
```

```yaml
version: '3'
tasks:
  wfsys:up:   # ds:def id=tasks-wfsys-up-k2jzmkg9
    desc: start the stack
    cmds:
      - docker compose up -d
```

| id | symbol | lines |
|---|---|---|
| `server-port-6btxuz6q` | `server.port` | 2 |
| `server-hosts-rkm9nch2` | `server.hosts` | 3-5 |
| `auth-kkwgh3ax` | `auth` | 6-7 |
| `tasks-wfsys-up-k2jzmkg9` | `tasks.wfsys:up` | 3-6 |

- A scalar key binds its line, and its value is a fact (`8081`).
- A key holding a map or a list binds the key and everything nested under it.
- The symbol is the full key path. A colon inside a key is part of it (`tasks.wfsys:up` needs no quoting); a key containing a dot is quoted, `tasks."a.b".desc`.
- A directive on its own line directly above a key binds that key too:

```yaml
server:
  # ds:def id=yaml-above-j3k4m5n6
  port: 8081
```

Caveat: `span=` is ignored in YAML; a def binds the key it names and its children.

## TOML

Comment: `#`. Tier: `toml`.

```toml
title = "app"

[server]   # ds:def id=server-2sqn3xnu
port = 8081   # ds:def id=server-port-tyut4pyc
host = "0.0.0.0"

[database.pool]
max = 20   # ds:def id=database-pool-max-rt7eqd8s
```

| id | symbol | lines |
|---|---|---|
| `server-2sqn3xnu` | `server` | 3-5 |
| `server-port-tyut4pyc` | `server.port` | 4 |
| `database-pool-max-rt7eqd8s` | `database.pool.max` | 8 |

- A key binds its line, named by its table path, and its value is a fact (`8081`, `20`).
- A table header binds the table, up to the next table.
- By symbol, `ds def path#server.port` and `path#database.pool.max` work; a table itself (`#server`) is not found by symbol, so define it with `ds def path:N` on the `[server]` line.

Caveat: `span=` is ignored in TOML.

## HCL and Terraform

Comment: `#` (or `//`). Tier: `hcl`. Files: `.tf`, `.hcl`; `.tfvars` is read too, but see the caveat.

```hcl
# ds:def id=variable-region-72rabbry
variable "region" {
  # ds:def id=variable-region-default-gb3cbd34
  default = "us-east-1"
}

# ds:def id=resource-aws-instance-web-dc2ucbjp
resource "aws_instance" "web" {
  # ds:def id=resource-aws-instance-web-instance-type-vz2gxfnb
  instance_type = "t3.micro"
  tags = {
    Name = "web"
  }
}
```

| id | symbol | lines |
|---|---|---|
| `variable-region-72rabbry` | `variable.region` | 2-5 |
| `variable-region-default-gb3cbd34` | `variable.region.default` | 4 |
| `resource-aws-instance-web-dc2ucbjp` | `resource.aws_instance.web` | 8-14 |
| `resource-aws-instance-web-instance-type-vz2gxfnb` | `resource.aws_instance.web.instance_type` | 10 |

- A block binds through its closing brace, named by its type and labels.
- An attribute binds its line and is a fact (`us-east-1`, `t3.micro`).
- Create defs with `ds def path:N`; `ds def path#resource.aws_instance.web` is not found by symbol even though that is the symbol recorded.
- An attribute inside a nested map (`Name = "web"` inside `tags = { … }`) cannot be bound: `ds def` refuses with `the directive would not bind`. Bind the map's attribute (`tags`) or the whole block.
- A remote def can pick an attribute out of any HCL file: `pick=hcl:resource.aws_instance.web.instance_type`.

Caveat: `ds def` refuses `.tfvars` files (`no comment carrier`), but a directive you write by hand is read: `x = 1 # ds:def id=tfv-x-d3e4f5g6` binds `x`. `span=` is ignored in HCL.

## INI, dotenv, properties and other config

Tier: `config`. Comment: `#`; INI and `.cfg` also accept `;`, properties also `!`. `ds def` writes the directive at the end of the key's line; a directive on its own line above the key binds it too.

```ini
[server]
port = 8081   # ds:def id=server-port-6dmz4e7y
host = localhost
```

```bash
# .env
API_HOST=api.example.com
API_PORT=8081   # ds:def id=api-port-5kdcdk3b
```

```properties
# ds:def id=prop-above-k3m4n5p6
server.port=8081
```

| File | Symbol | Value (`ds facts`) |
|---|---|---|
| `app.ini` | `server.port` (section.key) | `8081` |
| `.env` | `API_PORT` | `8081` |
| `app.properties` | `"server.port"` (quoted, because the key holds a dot) | `8081` |
| `nginx.conf` | none; binds the line | `listen 80;` |

- A key binds its line and its value is a fact.
- By symbol: `ds def app.ini#server.port` (or the bare key, `#port`), `ds def .env#API_PORT`, `ds def 'app.properties#"server.port"'`. A `.conf` file has no keys docsync knows, so use `ds def path:N`.
- `span=+N` widens a key's def to N more lines: `x = 1   # ds:def id=ini-span-m2n3p4q5 span=+1` binds lines 2-3.

Caveat: put the directive on the line above the key, not at the end of it, in any format whose parser does not accept a comment after a value. Java `.properties` files treat everything after the separator as the value, so `server.host:localhost   # ds:def id=…` (what `ds def` writes there) makes the value `localhost   # ds:def id=…` for Java; many INI parsers behave the same. Write those defs by hand on the line above, as in the properties example.

## Other code: Rust, Kotlin, Ruby, PHP, C, Java, shell, Dockerfile, Makefile and more

Tier: `code`. This is a heuristic binder for every language with a comment syntax but no grammar in docsync. It recognises common declaration shapes; it does not parse the language.

- **By symbol** it finds declarations that start with a keyword: Rust `fn`, `pub fn`, `struct`, `const`; Kotlin `fun`; Ruby `def`; PHP `function`. It does not find a declaration that starts with a type or modifiers, such as C's `int add(`, Java's `public class App`, C#'s `public int Add(`, Lua's `local function`, a shell `foo() {`, or a Makefile target. Use `ds def path:N` for those.
- **Extent**: a block with braces binds through its closing brace; anything else binds up to the next blank line. `span=+N` overrides that.

Rust, by symbol:

```rust
// ds:def id=max-conn-2gga7pp3
pub const MAX_CONN: u32 = 64;

// ds:def id=pool-7jx4mz8e
pub struct Pool {
    size: u32,
}

impl Pool {
    // ds:def id=new-7vbfuuyf
    pub fn new(size: u32) -> Self {
        Pool { size }
    }
}

// ds:def id=connect-jvx3a2y2
pub fn connect(url: &str) -> bool {
    !url.is_empty()
}
```

`MAX_CONN` is a fact (line 2); `Pool` binds lines 5-7; `new` lines 11-13; `connect` lines 17-19.

Java, by line (the label comes from the file name):

```java
// ds:def id=app-6nnzwk9h
public class App {
    // ds:def id=app-kymqyn9g
    public static final int PORT = 8080;

    // ds:def id=app-j6b4fgsy
    public void run() {
        System.out.println("run");
    }
}
```

The class binds lines 2-10, `PORT` line 4, `run` lines 7-9. These have no symbol in the ledger.

Shell, by line:

```bash
#!/usr/bin/env bash
set -euo pipefail

# ds:def id=deploy-afeg598k
REGION=us-east-1

# ds:def id=deploy-7vctpbk3
deploy() {
  echo "deploying to $REGION"
  kubectl apply -f k8s/
}

deploy
```

`REGION` binds line 5; the function binds lines 8-11.

Dockerfile and Makefile, by line. With no braces, a def binds up to the next blank line:

```dockerfile
# ds:def id=dockerfile-xt9v8d4t
FROM golang:1.23 AS build
WORKDIR /src
COPY . .
RUN go build -o /app ./cmd/app

FROM gcr.io/distroless/base
# ds:def id=dockerfile-mnw8fd4v
EXPOSE 8080
ENTRYPOINT ["/app"]
```

The build stage binds lines 2-5; the second def binds lines 9-10.

```makefile
# ds:def id=makefile-hzgqxfzh
PORT ?= 8080

# ds:def id=makefile-t9f63qt8
build:
	go build ./...

test:
	go test ./...
```

`PORT` binds line 2; the `build` target lines 5-6.

The comment form per extension: `//` for `.rs`, `.c`, `.h`, `.cpp`, `.java`, `.kt`, `.swift`, `.cs`, `.scala`, `.dart`, `.zig`, `.pkl`; `//` or `#` for `.php`; `#` for `.rb`, `.sh`, `.bash`, `.zsh`, `.pl`, `.r`, `.ex`, `.exs`, `.nix`, `Dockerfile`, `Makefile`; `--` for `.lua`, `.hs`; `/* … */` for `.css` (and `//` for `.scss`).

## Markdown and MDX

Comment: `<!-- … -->` in `.md` and `.markdown`; `{/* … */}` in `.mdx`, which `ds def` writes for you. Tier: `markdown`. In both, keep a directive on one line.

```
$ ds def 'doc/guide.md#Session policy'
$ ds def 'doc/guide.md#Rotation'
$ ds def doc/guide.md:13
$ ds def 'doc/page.mdx#Install'
```

```markdown
# Guide

<!-- ds:def id=session-policy-bhnkezkp -->
## Session policy

Sessions live thirty days.

<!-- ds:def id=rotation-e57wcygb -->
### Rotation

They rotate on refresh.

## Limits

<!-- ds:def id=guide-yw7batrr -->
Ten per user.
```

```mdx
# Page

{/* ds:def id=install-fpu5ds9d */}
## Install

Run the installer.
```

| id | kind | symbol | lines |
|---|---|---|---|
| `session-policy-bhnkezkp` | section | `Session policy` | 4-11 |
| `rotation-e57wcygb` | section | `Rotation` | 9-11 |
| `guide-yw7batrr` | paragraph | | 16 |
| `install-fpu5ds9d` | section | `Install` | 4-6 |

- A def above a heading binds the section: the heading and everything up to the next heading of the same or a higher level. `Session policy` includes its `### Rotation` subsection; the directive anchoring the subsection is left out of the section's hash, so adding it does not flag citations of the section.
- A def above a paragraph binds that paragraph, up to the next blank line.
- By symbol, `#Heading text` finds a heading; the match ignores case.
- An inline fact, `[8081](ds:def?id=…)`, binds just its link text; see [Directives](directives.md#facts-inline-defs-in-prose).
- `span=+N` overrides the extent: the line below the directive plus N more (`span=+1` above a three-line paragraph binds its first two lines).

## HTML, XML and SVG

Comment: `<!-- … -->`. Tier: `markdown`. `ds def path:N` binds the element that starts on line N, through its closing tag:

```html
<html>
<body>
<!-- ds:def id=section-akcteyup -->
<section id="pricing">
  <!-- ds:def id=p-j3cfrpnu -->
  <p>Pro costs $10.</p>
</section>
</body>
</html>
```

The `section` element binds lines 4-7 and the `p` element line 6. The symbol is the tag name (`section`, `p`), so define elements by line.

`.vue` and `.svelte` files take `<!-- … -->` in markup (and `//` or `/* … */` in script), and are read by the `text` tier: a def binds the following lines up to the next blank line.

## AsciiDoc and reStructuredText

Comment: `//` in AsciiDoc, `..` in rst. Tier: `document`. They bind like markdown: a heading binds its section, anything else its paragraph.

```asciidoc
= Guide

// ds:def id=setup-4s3rwrwy
== Setup

Install it.

== Usage
```

```rst
Guide
=====

.. ds:def id=setup-w6f5vzsm
Setup
-----

Install it.
```

`ds def 'doc/guide.adoc#Setup'` binds lines 4-6; `ds def 'doc/guide.rst#Setup'` binds lines 5-8 (the title, its underline and the body).

## Plain text

Files: `.txt`, `.text`, and any file with an extension docsync does not know. Tier: `text`. There is no comment syntax, so the directive is a line of its own, and renderers strip it. A def binds the following lines up to the next blank line; `span=+N` binds exactly N lines. Note the difference from the other tiers, where `span=+N` means the bound line plus N more.

```text
Rota

ds:def id=oncall-mk77sv2h
Week 37  alex
Week 38  someone

Escalation goes to the lead.
```

binds lines 4-5. With an explicit span:

```text
ds:def id=span-two-c2d3e4f5 span=+2
Week 37  alex
Week 38  someone
Week 39  third
```

binds lines 2-3.

`ds def path:N` writes the bare line into `.txt` and `.text` files. For an unknown extension, or an extensionless file such as `NOTES` or `LICENSE`, `ds def` refuses (`no comment carrier for this file type`), because it cannot tell whether the format has a syntax a bare line would break. A directive you add by hand to such a file is read by the `text` tier; if the file is a format with real syntax, use a remote def instead.

There are no symbols in plain text: `ds def doc/oncall.txt#Rota` answers `plain text has no symbols; use path:line`.

## JSON, CSV and other files without comments

JSON (`.json`, `.jsonl`, `.ndjson`, `.geojson`, `.webmanifest`), CSV and TSV, and `go.sum` have no comment syntax, so no directive can go inside them. `ds def` refuses:

```
$ ds def doc/app.json:1
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=doc/app.json pick=…`), …
```

Define the value from a file that can hold a comment, usually the doc that mentions it, with `file=` and `pick=`:

```markdown
<!-- ds:def id=json-port-p2q3r4s5 file=doc/app.json pick=json:$.server.port -->
<!-- ds:def id=csv-port-t2u3v4w5 file=doc/svc.csv pick=csv:r2c2 -->

Port [8081](ds:cfg?id=json-port-p2q3r4s5), csv [8081](ds:cfg?id=csv-port-t2u3v4w5).
```

`ds facts` shows both as `8081`, located at `doc/app.json:1` and `doc/svc.csv:2`. A remote def hashes only the picked value, so other edits to the file do not flag it. The same form binds a value in any file you would rather not add directives to, with `pick=yaml:…`, `toml:…`, `ini:…`, `env:…`, `hcl:…`, `line:N` or `regex:…`; the full list is in [Directives](directives.md#pick--take-one-value-or-range-out-of-a-block).

A bare `ds:def` line found inside a JSON file breaks it, and `ds check` says so:

```
doc/bad.json
  2	error    problem            scan: directive is not inside a comment: doc/bad.json has no comment syntax, so this line breaks the file; ds repair --apply removes it (bind the value with a remote def instead)
```

`ds repair` lists the fix and `ds repair --apply` makes it: the line is deleted from a file with no comment syntax, and commented out in a file that has one.

## Images and other whole files

For a file whose content cannot be picked apart, such as an image, a PDF or a generated file, a remote def with `pick=file` hashes the whole file:

```markdown
<!-- ds:def id=p-file-q2q2q2q2 file=t/a.txt pick=file -->
```

Any change to the file flags the sentences that cite it.

## Checking what was bound

After adding defs, three commands show what docsync made of them:

- `ds check --explain` lists every directive with the tier that read it, the kind of block, and the carrier:

```
WHERE               TIER      WHAT           CARRIER  ID
docs/policy.md:3    markdown  def section    comment  sess-policy-h2n8wq4t
docs/policy.md:14   markdown  def paragraph  comment  retention-para-w4x5y6z7
store/store.go:21   go        def func       comment  store-savesession-m6twuucd
```

- `ds read <id>` prints the block's body, so you can see exactly where it starts and ends.
- `.ds/ledger.tsv`, written by `ds scan`, records each id's kind, symbol and line range.

If a def binds more or less than you meant, move the directive, add `span=+N` where the tier honours it, or bind the value with `pick=`.
