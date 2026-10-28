# Container data mount warning

Candidate change, not yet released. The startup log no longer claims that a
mount is persistent merely because its mountinfo path differs from Docker's
default anonymous-volume path. Docker's generated-looking 64-character volume
paths are recognized under custom and rootless roots as well as the default
root. This is a warning, not proof that a volume is anonymous: a deliberately
named volume or a bind mount can have the same shape.

Mountinfo also cannot prove that a bind mount, named volume, filesystem-root
mount or a mount inherited from a parent will be reused when the container is
recreated. The log asks operators to check their container configuration and
retain their data before updating. A directory with no separate mount point may
be in the writable layer or on an ancestor mount. Matching mount paths with
escaped spaces are decoded; ambiguous overlapping mounts are not classified
as safe. The warning never blocks startup and does not change any database,
mount, backup or container configuration.

Candidate validation on 2026-10-28 includes focused Go tests/race/vet and an
isolated OVH Docker daemon with a non-default data root. A locally built
candidate binary starts with an automatically generated volume, a named
volume, a bind mount containing a space, and tmpfs; all four log the expected
classification without claiming persistence. The test uses synthetic data,
no container network or host ports, and removes its own daemon, containers,
volumes and image. It is not validation of a published binary or OC424 rollout.

Confirm the actual source and type with the container runtime, for example
`docker inspect` on the running container, and verify that the same reusable
volume or host directory is configured in the deployment definition. A
`tmpfs` mount or an anonymous volume attached to an existing container does
not become persistent merely because it appears in mountinfo. Retain a tested
backup before changing or recreating a container.
