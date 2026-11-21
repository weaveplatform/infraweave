# Generated pins: deploy/images/imagectl.py generate reads versions.json.
# /bin/terraform can be replaced by the launcher's read-only per-run tool mount.
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
ARG TARGETARCH
LABEL org.opencontainers.image.title="infraweave-runner-terraform" \
      org.opencontainers.image.description="infraweave runner with terraform installed" \
      org.opencontainers.image.version="1.17.2" \
      org.opencontainers.image.source="https://github.com/weaveplatform/infraweave"
RUN apk add --no-cache bash ca-certificates curl git jq openssh-client procps tzdata \
 && addgroup -g 1983 infraweave \
 && adduser -D -u 1983 -G infraweave -s /bin/bash infraweave \
 && install -d -o 1983 -g 1983 /mnt/workspace
RUN case "$TARGETARCH" in \
      amd64) sum=e6f16b2f0b137ee5729caa8c4e79a3bae4ee261acd198a1fcfa8584a6296605b ;; \
      arm64) sum=858e08c181a76b6b223e8c767a8e983922eebe2d062db949a2de66ce040c368c ;; \
      *) exit 1 ;; \
    esac \
 && curl --fail --location --retry 3 "https://releases.hashicorp.com/terraform/1.17.2/terraform_1.17.2_linux_${TARGETARCH}.zip" -o /tmp/tool.zip \
 && echo "$sum  /tmp/tool.zip" | sha256sum -c - \
 && unzip -q /tmp/tool.zip terraform -d /bin \
 && chmod 0755 /bin/terraform \
 && install -d /usr/share/licenses/terraform \
 && unzip -p /tmp/tool.zip LICENSE.txt > /usr/share/licenses/terraform/LICENSE \
 && rm /tmp/tool.zip
USER 1983:1983
WORKDIR /mnt/workspace
