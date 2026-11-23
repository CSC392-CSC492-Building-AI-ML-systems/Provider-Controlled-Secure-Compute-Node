# Continuous integration

The repository's GitHub Actions workflow is defined in
[`../.github/workflows/ci.yml`](../.github/workflows/ci.yml). It runs on every
push and pull request and performs the checks below.

## Go checks

The Go job uses the version declared in `go.mod` and runs:

```sh
go test ./...
go test -race ./internal/containerengine ./internal/lease ./cmd/node-agent
go vet ./...
```

The race job focuses on the lease and container lifecycle paths, which contain
the most concurrency-sensitive provider behavior.

## Web checks

The web job uses Node.js 20, installs the locked dependency set with `npm ci`,
then runs:

```sh
npm run lint
npm run build
```

The build compiles the TypeScript application and produces the production Vite
bundle.

## Local verification

Run the same checks from the repository root with Go installed, then run the web
commands from `web/`. Full Go tests that open local TCP or Unix sockets may be
blocked by restrictive local sandboxes; GitHub's Ubuntu runner provides the
normal host environment for those tests.
