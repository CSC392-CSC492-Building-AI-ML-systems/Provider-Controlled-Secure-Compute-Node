# Allowlist

The allowlist is essentially the provider's list of container images that are allow to run on the designated machine.

If the coordinator requests a specific job, the provider uses the allowlist to deiced whether that jobs is valid.

## Pre-Terminology

An **image** is essentially the packaged contents of the container. It contains everything the program needs. This includes the operating system files, libraries, the program itseld and other additional configurations.

A container is made from an image that actually runs.

A **digest** is hash value, basically the fingerprint of the image's exact contests. We refer to the image via the digest.

## How this connects?

We use digests to sore the approved jobs in the allowlist files. Each digest is unqiue, so certain leases with slightly different configurations can still be denied. This is a strict limiation. We use a Go map to store these digests.

Podman specifically uses digests to retrieve the image and compare. If they match, then it is able to run the container, otherwise refuses the image.

`checker.go` and `checker_test.go` implements this, and our allow list is stored on disk via a JSON file. Note that the JSON file does not exist yet. 
