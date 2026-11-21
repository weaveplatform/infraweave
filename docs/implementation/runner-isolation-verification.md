# Acceptance runner isolation

## Accepted image lifecycle

[#703](https://github.com/weaveplatform/infraweave/issues/703) records an actual
cross-suite failure caused by importing and force-removing the same temporary
`infraweave-runner:acceptance` tag. The accepted public-image implementation in
[#710](https://github.com/weaveplatform/infraweave/pull/710),
[#711](https://github.com/weaveplatform/infraweave/pull/711) and
[#714](https://github.com/weaveplatform/infraweave/pull/714) replaces that lifecycle.
Suites pull committed immutable GHCR digests, mount their current application
and helper executables, and remove their labelled containers. Images remain
cached across suite teardown. The original proposed per-owner temporary image
references and their deletion are superseded by this public-image design.

## Requirement evidence

| Requirement | Executable proof |
|---|---|
| Force the old failure at a controlled boundary | A unique private image alias recreates the old shared-tag deletion. Both owners start real tools with it; A removes its container and alias, B's existing tool stays running, and B's next create must return Docker's `No such image`. The same test then repeats the teardown boundary with retained immutable references and requires B's next tool and Terraform plan to succeed. No published/shared reference is removed. |
| Independent owners overlap against real Docker | `TestAccSharedImageLifecycle` starts A and B with separate clients and running containers, sharing the committed immutable image. |
| A finishes before B starts its next tool | A's scenario removal and repeated close precede B's next forwarder and actual provider-free Terraform init/plan. Container inspection checks the committed image and read-only tool mount. |
| Failed and canceled work preserves B | The same test cancels preparation, starts an invalid executable to retain a real failed container and its Docker diagnostic, cancels a wait on A's running tool, and removes A's labelled resources using a live cleanup context. B's running container survives. |
| Repeated cleanup removes only owned resources | Label-filtered readback finds no A or B containers after teardown; the shared image ID remains unchanged. `TestUnitScenarioCleanupFollowsAreaHooks` separately verifies failed-step/area cleanup order and retained failure evidence. |
| Cached image choice and rollback remain exact | Actual created containers use test-support, the retained previous Terraform digest, current Terraform and current OpenTofu in turn; inspection checks the requested reference. |
| Custom/default images, mirrors, caps and real workers | Existing runner-image, runtime-config, worker-pool and worker-protocol acceptance remains in the unchanged required CI partitions, including the complete certified-FIPS run. |

The regression is a `TestAcc` test beside the harness. `make accept PARTITION=core`
and the complete `make accept FIPS=1` execute it with
`INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1`; unavailable Docker fails the test.
The complete quality gate retains the existing race, FIPS and 95% total / 90%
per-package coverage thresholds. Final CI results must be linked before the
remaining #703 acceptance is declared complete.

The original native ARM64 overlap, image-selection and worker restart evidence
is recorded in [public runner image acceptance](../research/runner-image-acceptance.md).
It establishes the macOS forwarder path for the public-image implementation;
the added failed-start/cancellation cases require their own executed CI result.

## Local execution — 2026-11-21

The extended lifecycle passed on native ARM64 Docker Desktop 29.6.1 / API 1.55
in 60.44 seconds with the committed r2 image pins:

```sh
INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1 GOWORK=off \
GOFIPS140=certified GODEBUG=fips140=only \
go test -count=1 -timeout 10m -v \
  -run '^TestAccSharedImageLifecycle$' ./test/acceptance/internal/harness
```

The run explicitly reported that canceled preparation, failed startup and
canceled-wait cleanup preserved B's running container, then completed the real
Terraform plan, digest-selection/rollback assertions and empty owned-container
readback. Harness race/shuffle unit tests and documentation checks also passed.
The initial negative-executable probe exposed a test assumption: this Docker
runtime can acknowledge start before reporting the executable failure. The
assertion now accepts either start refusal or a nonzero process result, requires
a retained diagnostic, and verifies teardown against actual Docker state.
Full CI on the committed final head remains required.

The controlled legacy baseline and repaired comparison subsequently passed
together in **13.93 seconds** under certified FIPS on the same native ARM64
daemon. The baseline returned Docker's actual `No such image` at B's next create;
the repaired path completed Terraform init/plan and verified container cleanup
and retained shared images. The private baseline alias was absent after removal.
The [machine-readable candidate proof](runner-isolation-controlled-proof.json)
records the command, source context and log checksum. This closes the local
negative-control requirement; final exact-head CI remains required.
