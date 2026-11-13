# Changelog

- 2026-11-13
  - The weekly Tool drift job calls the Tool drift workflow v1.1.1, which also checks the `SNYK_VERSION` pin against the latest Snyk CLI release and opens an issue when it falls behind. Found internally from backlog item 546.

- 2026-11-12
  - Updated the indirect dependencies golang.org/x/net to v0.62.0 and golang.org/x/crypto to v0.59.0 (with x/sync v0.25.0, x/sys v0.50.0, and x/text v0.44.0). The release binary links the code of three advisories that govulncheck reports in binary mode: GO-2026-6866 and GO-2026-6860 in `golang.org/x/net/html`, and GO-2026-5932 in `golang.org/x/crypto/openpgp`. The source scan finds none of them called by the exporter; the binary now scans clean in both modes. Found internally from backlog item 547.
  - Raised the required Go version to 1.27.4, which fixes GO-2026-6859 (CVE-2026-97054), a crash of HTTP/1 servers in the standard library `net/http` package; govulncheck reports the affected code as reachable from the exporter's metrics server. Found internally from backlog item 547.
  - A Snyk job in continuous integration (CI) tests the exporter's Go module dependencies on every pull request, every push of `main`, and the weekly run, and fails on any high or critical issue, a scan error, or a missing `SNYK_TOKEN` secret. The Snyk CLI 1.1308.4 is downloaded from its GitHub release and verified by its checksum. Found internally.
  - The README shows status badges for the CI workflow on `main`, the Release workflow, and CodeQL code scanning.

- 2026-11-09
  - The weekly Tool drift job calls the Tool drift workflow v1.1.0, which also checks the Go version in `go.mod` and opens an issue when a newer patch release of its line exists or the line reaches its end of life. Found internally from backlog item 382.

- 2026-10-24
  - The continuous integration (CI) secret scan downloads gitleaks 8.30.1, up from 8.21.2, verified by its checksum, and the download accepts only HTTPS with Transport Layer Security (TLS) 1.2 or later (`curl --proto "=https" --tlsv1.2`), so a redirect cannot drop it to plain HTTP. Found internally from backlog item 381.

- 2026-10-23
  - The secret scan now covers the history of the checked-out commit (`--log-opts="HEAD"`), so a secret on another branch no longer fails every run; checkouts no longer keep their credentials.
  - A weekly Tool drift job opens an issue when the gitleaks pin falls behind its latest release, and a pre-commit push hook runs gitleaks over the commits being pushed.
  - Added SMART self-test metrics for Advanced Technology Attachment (ATA) and NVM Express (NVMe) disks, read from the output the SMART collector already fetches: `zfs_smart_self_test_passed` and `zfs_smart_self_test_age_seconds` for the newest completed or failed test of each type, and `zfs_smart_self_test_in_progress`. The ATA self-test log counts lifetime hours in 16 bits, so the age is corrected for wraparound.
  - Added the SmartSelfTestFailed and SmartSelfTestOverdue example alerts.
  - Work now merges into `main` by squash-merged pull request; the `develop` branch is retired. Release candidate tags go on `main` commits, and a release is promoted by tagging the release candidate's commit, with no fast-forward of `main`.
  - Dependabot now opens its pull requests against `main`, and `scripts/next-version.sh` reads `origin/main` by default.
  - CI tool versions now live where Dependabot updates them: the Python tools in `ci/requirements.txt`, actionlint and the golangci-lint version in `.pre-commit-config.yaml`, and govulncheck in a separate `ci/tools/go.mod` module that stays out of the exporter's build.
  - Dependabot now also watches `ci`, `ci/tools`, and the pre-commit hooks; gitleaks stays pinned with its checksum in the workflow.

- 2026-10-22
  - Raised the required Go version to 1.27.3, which fixes GO-2026-6809: the standard library `crypto/x509` package now distrusts compromised intermediate certificates, and the exporter's TLS support called into the affected code.
  - Continuous integration (CI) now also runs on every push of `main` and every Monday, so new vulnerability advisories surface without a commit.
  - Added a secret scan job that runs gitleaks over the full history of an unshallow clone.
  - The Lint job now runs yamllint, actionlint, and the remaining pre-commit hooks, so each documented gate runs on every pull request; the separate Check YAML workflow, which skipped pushes to open pull requests and unrelated file changes, is removed.
  - Pinned govulncheck, the lint tools, and the runner image (`ubuntu-24.04`) to versions instead of `latest`.

- 2026-10-14
  - `scripts/next-version.sh` prints the command that fast-forwards `main` without a release when only `ignore/` pull requests were merged since the last release.
  - Fixed the pool collector reporting a failure on a host with no ZFS pools: OpenZFS 2.4 prints nothing for `zpool list -j` there, which now counts as no pools. Empty `zpool list` output while `zpool status` reports pools is still an error.
  - Fixed the FreeBSD rc.d script leaving `service zpool_status_exporter start` or `restart` attached to the caller's terminal: daemon(8) now runs with `-f`, so the command returns over SSH. The exporter's output still goes to syslog.

- 2026-10-13
  - `scripts/next-version.sh` now reads squash-merged pull requests: it takes the pull request number from each commit subject and looks up its branch prefix with the GitHub CLI (gh).
  - Rewrote the exporter: pool, virtual device (vdev), scan, I/O rate and latency, Adaptive Replacement Cache (ARC), kernel statistic, dataset, and Self-Monitoring, Analysis and Reporting Technology (SMART) metrics under the `zfs_` namespace. Every earlier metric name is replaced.
  - Collectors now run in the background on their own intervals (10 seconds; datasets 1 minute; SMART 10 minutes), and scrapes return their latest results.
  - Pool and vdev data now come from `zpool status -j` and `zpool list -j`, which requires OpenZFS 2.3 or later.
  - Every vdev series now carries its pool, vdev, type, allocation class, partition, and disk, and `zfs_disk_info` and `zfs_smart_device_info` carry model, serial number, and World Wide Name (WWN).
  - The SMART collector no longer wakes disks in standby, skips enclosure devices, and reports smartctl exit status bits as metrics instead of failures.
  - The default port is now 10058, and command-line flags now use kingpin with the Prometheus exporter toolkit, which adds optional Transport Layer Security (TLS) and basic authentication.
  - Added Linux support on a best-effort basis, tested against synthesized data only.
  - Added FreeBSD rc.d and Linux systemd service files, FreeBSD package and RPM (Red Hat Package Manager) package builds, a manual page, example alerting rules, and an example Grafana dashboard that is not packaged.
  - Added tests that run every collector against output captured on a FreeBSD 15.1 host, and a check that keeps the README metric table in step with the code.
  - Replaced the release workflow: signed release candidate tags on `develop` build and publish the packages, and promoting a release candidate to `main` republishes the same files after checking their checksums and attestations.
  - Replaced the removed `::set-output` workflow command, added Dependabot for GitHub Actions and Go modules, and added version comments to pinned action commits.
