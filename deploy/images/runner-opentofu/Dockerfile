# Generated pins: deploy/images/imagectl.py generate reads versions.json.
# /bin/tofu can be replaced by the launcher's read-only per-run tool mount.
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
ARG TARGETARCH
LABEL org.opencontainers.image.title="infraweave-runner-opentofu" \
      org.opencontainers.image.description="infraweave runner with opentofu installed" \
      org.opencontainers.image.version="1.13.10" \
      org.opencontainers.image.source="https://github.com/weaveplatform/infraweave"
RUN apk add --no-cache bash ca-certificates curl git jq openssh-client procps tzdata \
 && addgroup -g 1983 infraweave \
 && adduser -D -u 1983 -G infraweave -s /bin/bash infraweave \
 && install -d -o 1983 -g 1983 /mnt/workspace
RUN case "$TARGETARCH" in \
      amd64) sum=c1f0d6b34d8328f963d3b1ad10896fae30a6c6890596190ffef477dab991090f ;; \
      arm64) sum=e8febf96590c59f40bda6a3ea152847eebea8ec5efe7a39ba1d53a979e230cb3 ;; \
      *) exit 1 ;; \
    esac \
 && curl --fail --location --retry 3 "https://github.com/opentofu/opentofu/releases/download/v1.13.10/tofu_1.13.10_linux_${TARGETARCH}.zip" -o /tmp/tool.zip \
 && echo "$sum  /tmp/tool.zip" | sha256sum -c - \
 && unzip -q /tmp/tool.zip tofu -d /bin \
 && chmod 0755 /bin/tofu \
 && install -d /usr/share/licenses/opentofu \
 && unzip -p /tmp/tool.zip LICENSE > /usr/share/licenses/opentofu/LICENSE \
 && rm /tmp/tool.zip
USER 1983:1983
WORKDIR /mnt/workspace
