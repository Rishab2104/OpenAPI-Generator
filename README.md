# API Warden

Generates an OpenAPI 3.0 spec for a backend, for any framework, **without AI/LLMs**.
Everything is deterministic: parsing, traffic capture, pattern matching and boundary probing.
Three evidence sources feed one spec: the source code (what the API *requires*), recorded
traffic (what it *accepted*) and its error replies (*where the limits are*).

Written in Go; builds to a single static binary (no cgo).

## Status

| Step | What | State |
|---|---|---|
| 0 | Use a spec the backend already publishes (`apigen.py`, ported and fixed) | done |
| 1 | Routes from source: Express, Flask | done |
| 2–5 | Run locally, record, fuzz, filter real hits | next |
| A/B/C | Body fields, validation rules, recorded workflows | planned |
| 6–7 | Convert + overlay, validate, coverage report | planned |

## Build

```bash
go build -o api-warden ./cmd/api-warden        # api-warden.exe on Windows
go test ./...
```

## Usage

```bash
# Step 0: does the backend publish its own spec? (only sends GET requests)
api-warden spec localhost:3000            # -> warden-out/step0_spec.yaml (+ crawled_routes.txt)

# Step 1: routes from source
api-warden scan path/to/backend           # -> warden-out/routes.txt, warden-out/routes.json
api-warden scan path/to/backend --extra-routes warden-out/crawled_routes.txt
api-warden rules                          # list framework rule sets
```

Local hosts (`localhost`, `127.0.0.1`, `*.test`, private IPs, docker service names)
default to `http://`.

`routes.txt` holds one sendable path per line (`/users/{id}` becomes `/users/1`, uuids get
a valid uuid, enums their first value, everything else `test`), ready for
`wfuzz -z file,routes.txt`. `routes.json` keeps the templated paths, parameter types, and
the file and line each route came from.

## How source is read

No tree-sitter (it needs cgo). Instead each language has an exact lexer:
comments, every string form (JS templates and regex literals; Python prefixes,
triple quotes and f-strings) and brackets are tokenized precisely, so a route inside
a comment or a string is never picked up. Rules then match patterns on tokens and
follow imports across files: Express `app.use()` mounts, Flask blueprints and
`register_blueprint()` prefixes.

## Layout

```
cmd/api-warden/   CLI
internal/
  core/           language-agnostic: models, path helpers, project walker, Rule interface
  lang/js/        JavaScript/TypeScript lexer + static evaluation + module resolution
  lang/python/    Python lexer + static evaluation + import resolution
  rules/          one file per framework, registered from init()
  scan/           step 1 orchestration and outputs
  step0/          discovery, Swagger 2 -> OpenAPI 3 conversion, crawler
  ordered/        order-preserving JSON/YAML documents
testdata/         fixture Express and Flask apps, a Swagger 2 spec
```

Adding a framework means adding one file to `internal/rules/`: implement `core.Rule`
(`Name`, `Language`, `Detect`, `Scan`) and call `Register` from `init()`.
