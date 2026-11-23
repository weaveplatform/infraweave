# The infraweave server image: the same certified FIPS binaries as the
# worker image, on the same distroless static base, whose system trust roots
# verify TLS to PostgreSQL (sslrootcert=system), object storage and the key
# services. It holds no shell and writes nothing outside the paths its
# configuration names, so it runs with a read-only root file system. Its
# entrypoint is `infraweave all`, a complete installation in one process, so
# a deployment passes only --config; a deployment that splits the process
# modes replaces the entrypoint with `infraweave serve` or `infraweave
# scheduler`. The API listens on 8080 and the health probes on
# server.admin_listen, 9090, their defaults.
# ref: decision 0020
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
ARG TARGETARCH
COPY --chmod=0555 infraweave-linux-${TARGETARCH} /usr/local/bin/infraweave
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/infraweave", "all"]
