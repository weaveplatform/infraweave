FROM scratch
LABEL org.opencontainers.image.title="infraweave-runner-test-support" \
      org.opencontainers.image.source="https://github.com/weaveplatform/infraweave"
COPY --chown=1983:1983 rootfs/ /
USER 1983:1983
WORKDIR /mnt/workspace
