# Provider-Controlled Secure Compute Node

A hardened Linux compute node that lets an organization safely contribute GPUs to a federated GPU commons, while keeping full control over its own machines.

## Table of Contents

- [Background](#background)
- [Description](#description)
- [Relationship to Project 7b (Coordinator)](#relationship-to-project-7b-coordinator)
- [Node Lifecycle](#node-lifecycle)
- [Scope (MVP)](#scope-mvp)
- [Outcome](#outcome)
- [Deliverables](#deliverables)

## Background

The biggest challenge in building a federated GPU commons is **trust**. Organizations will only share GPUs if they:

- retain control over their machines,
- avoid exposing internal systems, and
- can reclaim resources at any time.

This project builds the **secure provider node** that allows a machine to safely participate in the federation. [Project 7b (Group 16)](#relationship-to-project-7b-coordinator) builds the **coordinator** that manages these nodes.

## Description

Students build a hardened Linux compute node using technologies such as **rootless Podman**, **Tailscale/Headscale**, and **Ollama/llama.cpp**. The node should:

1. **Register** with the federation and report capabilities (GPU, VRAM, drivers, runtimes).
2. **Run only allowlisted, digest-pinned** container workloads.
3. **Communicate** with the coordinator through heartbeat and lease protocols.
4. **Support provider-controlled** accept/reject, drain, and reclaim operations.
5. **Protect the host** through container isolation and private networking.

## Relationship to Project 7b (Coordinator)

This repository is the **node (provider) side** of the federation. **Project 7b (Group 16)** builds the **coordinator**, the central service that schedules work across all registered nodes. The two projects meet at the registration, heartbeat, and lease protocols.

### What the coordinator does

| Responsibility | Description |
| --- | --- |
| **Node registry** | Accepts node registrations and stores each node's reported capabilities (GPU model, VRAM, drivers, runtimes). |
| **Health tracking** | Consumes node heartbeats and marks nodes unhealthy or lost when heartbeats stop. |
| **Scheduling** | Matches queued inference jobs to eligible nodes based on capabilities, availability, and the provider's accept/reject and drain state. |
| **Lease management** | Grants time-bound leases to nodes, tracks their expiry or renewal, and reassigns work when a lease lapses. |
| **Reclaim handling** | Honors provider-initiated drain and reclaim, so no new work is scheduled and running work is wound down. |

The coordinator decides *what should run where*. The node always has the final say on whether to run it.

## Node Lifecycle

The node implements the following lifecycle states:

`register` → `heartbeat` → `lease` → `execute` → `complete` → `drain`

Failure paths are also handled and documented: container crashes, lost heartbeat, and emergency reclaim.

## Scope (MVP)

The MVP focuses on **curated inference workloads only**. The following are out of scope:

- Training
- Notebooks
- Arbitrary code execution

## Outcome

A deployable prototype of a secure compute node that demonstrates provider-controlled participation in a GPU commons. The design should be suitable for future adoption by organizations such as UofT labs, nonprofits, and research groups contributing underused GPUs.

## Deliverables

- [ ] **Working compute node software** with registration, heartbeat, lease, execution, and drain lifecycle
- [ ] **Single-machine demonstration:** node registers, receives a lease, runs allowlisted inference containers, reports completion, and drains cleanly
- [ ] **Security checklist** covering protections such as rootless containers, image pinning, host isolation, and resource limits
- [ ] **Provider configuration guide** explaining setup, controls, and reclaim procedures
- [ ] **Failure-case documentation** covering container crashes, lost heartbeat, and emergency reclaim
