# Changelog

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
