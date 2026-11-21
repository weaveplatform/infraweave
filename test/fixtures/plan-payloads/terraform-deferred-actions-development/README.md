# Patched-development deferred-action fixture

This is **real CLI output from a locally patched experimental producer**. It is
not official-release support, a native stable-runner capture, or an inferred
positive JSON document. The maintainer approved this evidence class on
2026-11-21 while retaining official-release production as unproven.

The producer starts at official Terraform v1.17.2 commit
`ae094fc0ee6d4f3d2a990b40ac48a6d2dbaeb53e`, enables its documented development
experiment switch, and applies `producer.patch`. The patch supplies the deferred
trigger event, failure mode and resolved provider address that the graph node
omits. It changes no JSON serializer. The unmodified experimental build still
writes `INVALID_EVENT`, reproducing the public alpha's unreadable saved plan.
The source, patch, compiler, binary and raw document hashes are in
`provenance.json`. Both projects use only builtin Terraform data and pinned
`hashicorp/local` 2.9.1; no apply or action invocation was executed.

`plan.json` is unchanged `show -json` stdout for the resource-prerequisite case.
It contains a marked private string, an identical unmarked copy, public values,
unknowns, a deferred resource and a deferred-action envelope. The actual nested
action **has no config values or sensitivity/unknown masks**. Its whole invocation
is structural metadata. The `count-unknown` project provides a second unchanged
real document for unknown action cardinality. Do not add fabricated values to
either captured document. Authored composition of the existing real stable
action shape with this wrapper exercises nested mask handling separately.

Reproduce into a new directory with `sh reproduce.sh /absolute/new/output`.
Commands and stdout/stderr are retained beside each capture. The script only
plans and reads the saved plans. Timestamps make fresh outputs differ from the
recorded SHA-256; the checked-in hashes establish the original capture bytes.
The source build is for fixture production only: published runner images keep
official stable binaries, and the execution catalogue still rejects prereleases.

The acceptance fixture's workflow transports these unchanged bytes through the
real stable worker, sanitization, sealing, storage and typed policy projection.
That is compatibility evidence. It does not assert deferred execution or that
an unmodified release produced a readable deferred-action plan.
