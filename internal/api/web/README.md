# internal/api/web

The server's three page shells, plus the bundle Vite builds from `../../../web/src`.

| | |
|---|---|
| `app.html` / `setup.html` / `login.html` | page shells — structure only, no CSS or JS |
| `build/` | **generated** by `make ui`; hashed JS/CSS + `manifest.json` |

Each shell carries two placeholders, `__SAMO_STYLES__` and `__SAMO_SCRIPT__`,
which `pageSource` (in `../web.go`) fills at startup by reading the manifest.
The result is served by `html/template`, which the pages use for nothing else —
they contain no template actions at all.

Assets are served from `/assets/build/` with an immutable cache lifetime, which
is safe because every filename carries a content hash.

## build/ is committed on purpose

`go:embed` is a compile-time dependency: an absent `build/` is not a stale UI,
it is a build failure. Committing it keeps `go build ./...` and `go test ./...`
working for anyone with only Go installed, which matters for a project whose
whole shape is "one static binary".

The cost is drift — a change to `web/src` that never gets rebuilt. `make
ui-check` rebuilds and fails if the committed output differs, which is the
check to run in CI. The Docker image rebuilds the bundle in its own Node stage
rather than trusting the committed copy.

## Editing the UI

Source lives in `web/src`, not here. After changing it:

```bash
make ui
```

then commit both the source and the regenerated `build/`.
