---
title: "A sandbox your coding agent can't leak out of"
description: "Why we built poddle: a secretless broker that gives coding agents full access with no vendor secret inside the pod."
pubDate: 2026-08-20
author: "The poddle team"
---

Coding agents are astonishingly capable, and to do real work they need real access: your model-provider key, your git token, your CI credentials, your database password. The usual way to grant that access is to hand it over - keys in the environment, tokens on disk, secrets already sitting in the prompt. Once the agent (or a compromised dependency inside its sandbox) can read those, so can anything else in the box.

poddle takes a different route: **the real secret never enters the pod.**

## How it works

When you spin up a pod, the agent runs fully authenticated - but what lives inside is only a **revocable handle**, never the credential itself. Every outbound request passes through a **broker** that:

- injects the real provider key on the wire, so the pod never sees it;
- scrubs stray secrets out of the request body before it leaves;
- enforces a per-request egress policy, and records a tamper-evident audit trail.

Revoke a pod and its access dies at once - nothing to rotate, nothing to scrub from an image.

```bash
poddle up my-sandbox --identity work   # a fully-authed shell, no key inside it
poddle down my-sandbox                 # handle revoked, pod gone
```

## Yours, either way

poddle is open source (AGPL-3.0) and runs on your own infrastructure - local Podman today, a remote SSH host with one flag. Or start free in the managed cloud and let us run the broker, the vault, and the pods; the guarantee is identical either way.

This is where we will write about the parts worth explaining: the secretless-broker design, the threat model, egress governance, and the tradeoffs we made along the way.

If you want the short version, it is the line at the top - a sandbox your coding agent can't leak out of.
