FROM almalinux:9.7
ARG VERSION
ARG RPM
ARG RPM_PUBKEY
COPY ${RPM} /tmp/zpool-status-exporter.rpm
COPY ${RPM_PUBKEY} /tmp/rpm-signing.pub.asc
COPY packaging/test/check-rpm.sh /check-rpm.sh
RUN sh /check-rpm.sh "${VERSION}" /tmp/zpool-status-exporter.rpm /tmp/rpm-signing.pub.asc
USER nobody
RUN zpool-status-exporter --version
