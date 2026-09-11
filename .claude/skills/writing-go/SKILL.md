---
name: writing-go
description: The Go rules this project follows — the check to run after every edit, and the modern syntax to use on Go 1.26. Load before reading, writing or modifying any .go file.
---

# Writing Go here

Adapted from [ardanlabs/kronk](https://github.com/ardanlabs/kronk)'s
`.agents/default/skills/writing-go`, by way of the eumaeus repository next
door. Where this file differs from kronk, the difference is about this
repository and is marked **Here:**.

## The version

**Go 1.26.** One directive in `go.mod` with no separate `toolchain` line, so
CI installs exactly what is written there.

Use every feature up to and including 1.26. Never one from a later version, and
never an outdated pattern when a modern one exists.

## After editing any `.go` file

Run these against the package you changed. All must pass; fix the code rather
than suppressing the diagnostic.

```sh
make check
```

which is:

```sh
gofmt -s -w .
go vet ./...
staticcheck ./...
go build ./...
go test ./...
```

**Here:** `make check` runs over `./...`, not one package at a time. eumaeus
scopes it to a package because `staticcheck ./...` there surfaces twenty
findings in code nobody has touched in months. This module has none, and
keeping it that way is cheaper than inheriting the workaround.

## The rules that come up in this codebase

### `errors.AsType[T](err)`, not `errors.As(err, &target)` — 1.26

```go
// Before
var refusal backupbus.Invalid
if !errors.As(err, &refusal) {
    return err
}

// After
refusal, ok := errors.AsType[backupbus.Invalid](err)
if !ok {
    return err
}
```

Reach for this wherever a handler decides which kind of failure it is looking
at — a name that is empty, a commitment that is already cancelled.

### `strings.SplitSeq`, not `strings.Split`, when iterating — 1.24

```go
// Before
for _, part := range strings.Split(s, ",") {

// After
for part := range strings.SplitSeq(s, ",") {
```

Also `strings.FieldsSeq`, `bytes.SplitSeq`, `bytes.FieldsSeq`.

**Here:** a loop that needs the *index* keeps `strings.Split` — the Seq form
has no index to give, and a hand-kept counter is worse than the allocation it
saves. Parsing `NOTIFY_RECIPIENTS` does not need one.

### `wg.Go(fn)`, not `wg.Add(1)` + `go func() { defer wg.Done() }()` — 1.25

```go
// Before
wg.Add(1)

go func() {
    defer wg.Done()
    process(item)
}()

// After
wg.Go(func() {
    process(item)
})
```

### `t.Context()`, not `context.WithCancel(context.Background())`, in tests — 1.24

It is cancelled when the test ends, which is the thing the deferred `cancel()`
was for.

### `omitzero`, not `omitempty`, for a `time.Time`, `time.Duration`, struct, slice or map — 1.24

`omitempty` never worked on a `time.Duration` or a `time.Time`.

**Here:** the widget's JSON is small and mostly scalars, where `omitempty`
does what it says. This rule is about the types it was silently wrong for.

### `b.Loop()`, not `for i := 0; i < b.N; i++`, in benchmarks — 1.24

**Here:** there are no benchmarks and probably never will be. Write any new
one this way.

### `new(val)`, not `x := val; &x` — 1.26

`new` takes an expression now, and the type is inferred: `new(30)` is a
`*int`, `new(true)` a `*bool`. Useful for the optional fields in a config
struct.

## Prefer the modern standard library generally

`slices`, `maps` and `cmp` over hand-written loops and sort helpers — and over
a helper of our own that does the same thing. Verify an API against the
toolchain or the docs rather than from memory; `make sym NAME=…` answers for
anything in this module.
