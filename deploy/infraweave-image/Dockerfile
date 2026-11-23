# The infraweave image: the static infraweave binary the build context
# holds for the target architecture, built with GOFIPS140=certified, on
# distroless static, which adds the system trust roots and nothing that
# runs. build.py builds the binaries and this image; the worker image is
# this recipe, and bootstrap init containers copy the binary from
# /usr/local/bin, where the Kubernetes executor expects it.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
ARG TARGETARCH
ARG RUN_AS=1983:1983
COPY --chmod=0555 infraweave-linux-${TARGETARCH} /usr/local/bin/infraweave
USER ${RUN_AS}
ENTRYPOINT ["/usr/local/bin/infraweave"]
