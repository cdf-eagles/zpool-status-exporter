# zpool-status-exporter: repository conventions

Repository-specific rules only. Rules shared by every repository (backlog,
evidence, delivery, and public-repository writing rules) live outside this
repository and are not repeated here.

## Scope and design

- Prometheus exporter for ZFS. FreeBSD (amd64) is the primary target; Linux
  (amd64) is best effort until it is tested on a live host.
- Data comes from command output (`zpool`, `zfs`, `smartctl`, `geom`,
  `glabel`, `lsblk`, `sysctl`) and the Linux kstat files. Prefer JSON output
  (`-j --json-int`) where OpenZFS offers it; `zpool iostat` has none in
  OpenZFS 2.4, so it is parsed as `-Hp` text.
- Operating-system code sits behind the `collector.Platform` interface. Both
  implementations are untagged so tests and linters cover them on any host;
  only `DefaultPlatform` uses build tags. Builds use `CGO_ENABLED=0`.
- Collectors run on schedules in the background; scrapes never run commands.
- Metric names use the `zfs_` namespace and the Prometheus naming rules (base
  units, `_total` only on counters, ratios from 0 to 1). Register every fixed
  metric through `gauge`/`counter` in the collector package, and regenerate
  the README table with
  `UPDATE_README=1 go test ./internal/collector -run TestReadmeMetricTable`.
- Libraries: `prometheus/client_golang`, `prometheus/common`,
  `prometheus/exporter-toolkit`, and `alecthomas/kingpin/v2`. Keep `go.mod` on
  the latest stable Go release.

## Tests

- Every external command goes through `runner.Runner`; tests use
  `runner.Fake`, and `runner.Exec` is tested with a helper process.
- Test data is real output captured from a host, with serial numbers, WWNs,
  and pool GUIDs replaced (`internal/collector/testdata/freebsd`). Never write
  fixtures from an assumed format; Linux fixtures are the documented
  exception until a Linux host is available.
- A new test must fail against the code before the fix.
- Gates before staging: `go test -race ./...` (coverage at least 90 percent),
  `golangci-lint run ./...` for `GOOS=linux` and `GOOS=freebsd` with the
  committed `.golangci.yml`, `go tool -modfile=ci/tools/go.mod govulncheck ./...`,
  `shellcheck -s sh` on every shell script, `yamllint .`, `actionlint`, and
  `pre-commit run --all-files`.
  golangci-lint must be built with the Go version in `go.mod`.
- CI tool versions live in `ci/requirements.txt`, `.pre-commit-config.yaml`,
  and `ci/tools/go.mod`, where Dependabot updates them; never pin one in a
  workflow `env:` block unless it is a release download verified by checksum.

## Scripts and packaging

- Support scripts are POSIX `sh` and shellcheck-clean; they run on macOS and
  FreeBSD, so no GNU-only options. Python, if ever needed, is 3.12 or later
  with type hints and pylint.
- The exporter runs as root (smartctl needs raw disk access).
- Packaging: `packaging/freebsd` (rc.d script, manifest, `build-pkg.sh`),
  `packaging/linux` (systemd unit, sysconfig, scriptlets), and
  `packaging/nfpm.yaml` (RPM). Keep the manual page in `docs/` in step with
  the flags.

## Branches and releases

- `main` and `develop`. Work branches use the prefixes `fix/`, `feat/`,
  `major/`, `build/`, and `ignore/` and are squash merged into `develop` by
  pull request. The prefix decides the version bump; `next-version.sh` reads
  it from each squash commit's pull request number with `gh`.
- `scripts/next-version.sh` proposes the next version. A signed
  `vX.Y.Z-rc.N` tag on `develop` builds and publishes a pre-release; release
  candidate artifacts carry the plain `X.Y.Z` version so they can be promoted
  unchanged.
- Promotion: fast-forward `main` to the release candidate commit (no pull
  request merge), then push a signed `vX.Y.Z` tag on the same commit. The
  release workflow republishes the release candidate's files after checking
  their checksums and attestations.
- `ignore/`-only promotion: when every pull request since the last release
  came from an `ignore/` branch, `main` may be fast-forwarded to `develop`
  without a tag; `next-version.sh` detects this and prints the command. Any
  other branch type since the last release (including `build/` and
  `dependabot/`) blocks it until a release.
