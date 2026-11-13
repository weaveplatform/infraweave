# infraweave's runner image for Terraform stacks: the tools a run's phases
# and hooks use, and no Terraform binary, which the launcher downloads,
# verifies and mounts at /bin/terraform at the release each run names. It
# meets the runner image contract that contract.sh checks: UID and GID
# 1983, ps, and nothing that relies on ENTRYPOINT or CMD, which the
# launcher replaces. runner-terraform and runner-opentofu differ only in
# these comments and their labels, since neither carries its tool.
# ref: decision 0008 § "Runner images"
FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

LABEL org.opencontainers.image.title="infraweave-runner-terraform" \
      org.opencontainers.image.description="infraweave's runner image for Terraform stacks" \
      org.opencontainers.image.source="https://github.com/weaveplatform/infraweave"

# procps replaces BusyBox's ps, which takes few of the options a hook's
# script may pass.
RUN apk add --no-cache bash ca-certificates curl git jq openssh-client procps tzdata \
 && addgroup -g 1983 infraweave \
 && adduser -D -u 1983 -G infraweave -s /bin/bash infraweave \
 && install -d -o 1983 -g 1983 /mnt/workspace

# USER names the UID and GID rather than the user, so Kubernetes'
# runAsNonRoot can tell the image does not run as root.
USER 1983:1983
WORKDIR /mnt/workspace
