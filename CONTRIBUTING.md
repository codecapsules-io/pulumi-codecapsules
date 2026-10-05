# Contributing

This provider is implemented in Go (using [`pulumi-go-provider`](https://github.com/pulumi/pulumi-go-provider)), with the TypeScript and Python SDKs generated from it via `pulumi package gen-sdk`. You never need to touch Go to use the SDKs — this page is for anyone building or extending the provider itself.

## Project layout

```
provider/            # Go provider source
  pkg/client/         # REST client for the Code Capsules platform API
  pkg/provider/        # Pulumi resources (Team, Space, MysqlCapsule, ...)
  cmd/pulumi-resource-codecapsules/  # plugin binary entry point
sdk/nodejs/  sdk/python/   # generated SDKs - do not hand-edit, see below
```

## Running the tests

No credentials and no network access are required — everything runs against an in-memory fake backend:

```bash
cd provider
go test ./...          # all packages
go test ./... -race    # with the race detector
go vet ./...
```

## Building the provider and regenerating the SDKs

```bash
cd provider
go build -ldflags "-X main.version=X.Y.Z" -o bin/pulumi-resource-codecapsules ./cmd/pulumi-resource-codecapsules

cd ..
pulumi package gen-sdk ./provider/bin/pulumi-resource-codecapsules --language nodejs --out sdk
pulumi package gen-sdk ./provider/bin/pulumi-resource-codecapsules --language python --out sdk
pulumi package get-schema ./provider/bin/pulumi-resource-codecapsules > provider/schema.json
```

`provider/schema.json` is checked in so schema changes are visible in code review.

**`gen-sdk` fully regenerates `sdk/nodejs/package.json` and `sdk/python/pyproject.toml` from scratch on every run.** Package name, license, keywords, and other metadata are set in `provider/pkg/provider/provider.go` (via `WithLanguageMap`, `WithLicense`, etc.) — not hand-edited into the generated files, since that gets silently overwritten the next time `gen-sdk` runs. If you need to change any of that metadata, change it in `provider.go`.

## Known issues (pulumi-go-provider v1.6.0)

Worth knowing if you extend this provider:

- **`secret` and `replaceOnChanges` struct tags must go in the `provider:"..."` namespace, not `pulumi:"..."`.** Putting `secret` in the `pulumi` tag panics as soon as the provider configures. Putting `replaceOnChanges` there is silently ignored — it looks correct until you test a replace. `pulumi:"apiKey" provider:"secret"` is the correct form.
- **The default, tag-driven `Diff` never marks a replace as delete-before-create.** For any resource with a server-enforced unique field (like a slug), this means a replace tries to create the new resource before deleting the old one and fails with a conflict. This provider's resources hand-write `Diff` with `DeleteBeforeReplace: true` instead of relying on `replaceOnChanges` tags.

## Publishing a release

1. Pick a version (semver, e.g. `0.1.0`) and rebuild + regenerate as above with that exact value — the provider binary and both SDKs must carry the same version.
2. The nodejs SDK needs one extra step before publishing: `tsc` compiles into `sdk/nodejs/bin/`, which is gitignored, but doesn't copy `package.json`/`README.md`/`LICENSE` into it. Before publishing:
   ```bash
   cd sdk/nodejs
   npm install
   npm run build          # tsc, then copies package.json/README.md/LICENSE into bin/
   cd bin
   npm publish --access public
   ```
3. Python needs one extra step too: `gen-sdk` writes `sdk/python/pulumi_codecapsules/README.md` (nested inside the package), but `pyproject.toml`'s `readme = "README.md"` expects it at `sdk/python/README.md` (next to `pyproject.toml`) — without copying it there, the build still succeeds but the PyPI page ends up with no description.
   ```bash
   cd sdk/python
   cp pulumi_codecapsules/README.md README.md
   python -m build
   twine check dist/*
   twine upload dist/*
   ```
4. Tag the release `vX.Y.Z` and attach cross-platform provider binaries to a GitHub Release — this is how `pulumi up` auto-downloads the matching plugin for either SDK:
   ```bash
   cd provider
   for target in darwin:amd64 darwin:arm64 linux:amd64 linux:arm64; do
     os="${target%%:*}"; arch="${target##*:}"
     GOOS=$os GOARCH=$arch go build -ldflags "-X main.version=X.Y.Z" -o pulumi-resource-codecapsules ./cmd/pulumi-resource-codecapsules
     tar -czf "pulumi-resource-codecapsules-vX.Y.Z-$os-$arch.tar.gz" pulumi-resource-codecapsules
   done
   GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=X.Y.Z" -o pulumi-resource-codecapsules.exe ./cmd/pulumi-resource-codecapsules
   zip pulumi-resource-codecapsules-vX.Y.Z-windows-amd64.zip pulumi-resource-codecapsules.exe
   gh release create vX.Y.Z pulumi-resource-codecapsules-*.tar.gz pulumi-resource-codecapsules-*.zip --title "vX.Y.Z"
   ```

## Roadmap

- `Domain` resource (custom domains per space).
- Additional capsule types beyond MySQL/Redis/storage/WordPress.
- `pulumi import` support for more resources (currently just `Team`).
