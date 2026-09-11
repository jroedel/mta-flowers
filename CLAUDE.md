# Working in this repository

One Go binary and a SQLite file, behind Caddy on one Vultr instance, that
collects flower commitments for the Feast of Our Lady of Schoenstatt on
**Saturday 17 October 2026**. Read [`docs/scope.md`](docs/scope.md) first — it
records what this is, what was deliberately left out, and why.

**The deadline is a feast day and does not move.** When a choice is between
finished and elegant, finish it.

## Touching Go: load the skill, then run the check

**Before reading, writing or modifying any `.go` file**, load
`.claude/skills/writing-go/SKILL.md` — the modern-Go rules for the version in
`go.mod`, which is 1.26.

**After modifying any `.go` file:**

```sh
make check
```

which is `gofmt -s -w`, `go vet`, `staticcheck`, `go build ./...` and the
tests. All must pass. **Fix the code; do not suppress the diagnostic.**

Unlike the project this convention came from, `make check` runs over `./...`
rather than one package. This module is small and has no inherited findings, so
a clean `staticcheck ./...` is achievable and worth keeping that way.

## A diagnostic is an address. Go to it.

`go build`, `go vet`, `go test` and `gofmt` all report `file.go:line:col`.
That is the answer to "where", and no search is needed to find it. Read the
region around the reported line directly — `sed -n '<line-8>,<line+8>p'` — and
edit. The most expensive habit available here is grepping for something the
compiler has already named.

## Find a Go symbol with `make sym`, not with grep

```sh
make sym NAME=Commitment          # declaration + doc comment + file:line
make outline FILE=internal/store/store.go
```

Both are `gopls`, which answers from the same type information the compiler
uses. `make dev-tools` installs it if missing; there is also a gopls MCP server
in some setups (`mcp__gopls__go_search`, `go_symbol_references`), which is the
same answer without the shell.

**grep is still right** for everything that is not a Go symbol: a setting name,
a SQL column, a string in a template, a word in `docs/`.

## Where things live

```
main.go                config from the environment, wiring, graceful shutdown
internal/store/        SQLite: schema, commitments, settings, admin sessions
internal/mail/         SMTP submission to Hetzner. The only outbound network call
internal/web/          handlers, templates, and the widget script it serves
deploy/                systemd unit, Caddyfile, cloud-init, installer
.github/workflows/     ci.yml, and deploy.yml which ships every push to main
docs/                  the reasoning that does not fit in a comment
```

A question about *what a guest sees* starts in `internal/web`. A question about
*what is stored* starts in `internal/store`.

## Two things this project does not do

**No secret ceremony.** One 0600 file, `~/.config/mta-flowers.env`, mirrored to
`/etc/mta-flowers.env` on the server. There is no vault and no encryption at
rest, because the data is a list of names of people bringing flowers. Do not
add one.

**No backups.** The admin page has a CSV download, and that is the whole
retention story. See `docs/scope.md` § Risks.

## Admin work belongs in the browser

Everything an organiser does — change the date, reset the count, remove
duplicates, download the list — is a page. **Do not add a CLI subcommand for
it.** The command line here is for deploying and provisioning only.

## House style

Comments explain **why**, at length, and in prose — including what was tried
and rejected, and what a reader would otherwise assume. Match the density of
the file being edited. Error sentences that reach a person say what to do about
it and never name a Go package. Everything in `git` is a pull request on a
branch; nothing is committed to `main` directly — and note that `main` deploys,
so a merge is a release.
