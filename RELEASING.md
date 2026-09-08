# Releasing

Cutting a release is a tag push. Everything below exists because something here
was once missed and shipped a broken first run.

## Before the tag

1. `make test`, `make lint`, `make validate`, `make check-versions` all clean, and
   the `ui/` four: `bun run lint`, `bun run typecheck`, `bun run test`,
   `bun run build`.
2. `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` reports no
   vulnerabilities.
3. Bump `version` and `appVersion` in `deploy/helm/xdlc-agent/Chart.yaml`, then run
   `./scripts/check-version-refs.sh`. It fails until every copy-pasteable release
   reference in `README.md` matches the new `appVersion`. Bump them together.

   Install, getting-started and deployment pins live in the
   [documentation](https://github.com/xdlc-labs/documentation) repository.
   Update those in the same release window, or the hosted guides stay on the
   old tag.
4. Move the `## [Unreleased]` block in `CHANGELOG.md` under the new version with a
   date.
5. **Walk the documented first run on a clean machine or an empty directory.** Not
   the test suite — the literal commands a new user copies out of the README:
   `xdlc init`, then `xdlc doctor --config config.yaml --skip-network`, then
   `xdlc daemon --config config.yaml`. Then the documented `docker run`. A release
   whose quickstart does not start is worse than a delayed release, and CI does not
   check this.

## The tag

```sh
git tag vX.Y.Z && git push origin vX.Y.Z
```

That runs `.github/workflows/release.yml`: GoReleaser builds the binaries, builds
and pushes `ghcr.io/xdlc-labs/xdlc-agent:X.Y.Z` (no leading `v`, linux/amd64 only),
then a second job attaches an SPDX SBOM and keyless-cosign-signs the image.

The workflow embeds the console UI by committing `internal/console/dist` and moving
the tag locally, and never pushes that commit. So the commit SHA in
`xdlc --version` does not resolve in the public repository. That is expected.

## After the tag — the manual step CI cannot do

**Check that the GHCR package is public.** GitHub creates org container packages
private on first push, there is no REST endpoint to change it, and
`release.yml`'s `packages: write` does not affect it. A private package with a
public repository means every `docker run` in the README fails with
`unauthorized` for everyone outside the org, while the tag itself exists — so it
reads like a broken release rather than a permissions setting.

Verify the way an outsider sees it, with no credentials:

```sh
curl -s -o /dev/null -w '%{http_code}\n' \
  https://ghcr.io/v2/xdlc-labs/xdlc-agent/manifests/X.Y.Z   # want 200, not 401
```

If it returns 401: Organization → Packages → `xdlc-agent` → Package settings →
Change visibility → Public, and link the package to the repository so repo access
is inherited. Do not use `GET /orgs/xdlc-labs/packages?package_type=container` to
check this — it returns `[]` for a private package instead of listing it. Use
`gh api /orgs/xdlc-labs/packages/container/xdlc-agent --jq .visibility`.

Then confirm the release itself: the GitHub release has binaries, `checksums.txt`
and the SBOM attached, `scripts/install.sh` fetches the new tag, and
`docker run ghcr.io/xdlc-labs/xdlc-agent:X.Y.Z --version` prints it.

## Building the image from source

Building `deploy/Dockerfile` needs roughly **3 GB** of free image storage (about
4 GB if you also pull a release image). On Fedora and other hosts where
`/etc/containers/registries.conf` sets `short-name-mode = "enforcing"`, podman
refuses the unqualified base images with
`short-name resolution enforced but cannot prompt without a TTY`. Either fully
qualify them or point `CONTAINERS_REGISTRIES_CONF` at a config with
`unqualified-search-registries = ["docker.io"]`. Docker is unaffected.

`deploy/Dockerfile.release` is not a from-source build — GoReleaser only copies an
already-built binary into it.
