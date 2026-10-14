# Changelog

- 2026-10-13
  - Rewrote the exporter: pool, virtual device (vdev), scan, I/O rate and latency, Adaptive Replacement Cache (ARC), kernel statistic, dataset, and Self-Monitoring, Analysis and Reporting Technology (SMART) metrics under the `zfs_` namespace. Every earlier metric name is replaced.
  - Collectors now run in the background on their own intervals (10 seconds; datasets 1 minute; SMART 10 minutes), and scrapes return their latest results.
  - Pool and vdev data now come from `zpool status -j` and `zpool list -j`, which requires OpenZFS 2.3 or later.
  - Every vdev series now carries its pool, vdev, type, allocation class, partition, and disk, and `zfs_disk_info` and `zfs_smart_device_info` carry model, serial number, and World Wide Name (WWN).
  - The SMART collector no longer wakes disks in standby, skips enclosure devices, and reports smartctl exit status bits as metrics instead of failures.
  - The default port is now 10058, and command-line flags now use kingpin with the Prometheus exporter toolkit, which adds optional Transport Layer Security (TLS) and basic authentication.
  - Added Linux support on a best-effort basis, tested against synthesized data only.
  - Added FreeBSD rc.d and Linux systemd service files, FreeBSD package and RPM (Red Hat Package Manager) package builds, a manual page, and example alerting rules.
  - Added tests that run every collector against output captured on a FreeBSD 15.1 host, and a check that keeps the README metric table in step with the code.
  - Replaced the release workflow: signed release candidate tags on `develop` build and publish the packages, and promoting a release candidate to `main` republishes the same files after checking their checksums and attestations.
  - Replaced the removed `::set-output` workflow command, added Dependabot for GitHub Actions and Go modules, and added version comments to pinned action commits.
