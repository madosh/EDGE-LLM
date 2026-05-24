# Artifacts

This directory is mounted into the control-plane and served at `/artifacts/*`.

The `artifact-init` service in `docker-compose.yml` creates `sample-gemma-4-e2b.tar.gz`
and its accompanying `.sha256` sidecar file on first run.

## Anatomy of an artifact

```
sample-gemma-4-e2b.tar.gz        — stub tarball (fake model)
sample-gemma-4-e2b.tar.gz.sha256 — hex SHA-256, used by the agent for verification
```

The agent downloads the tarball, computes its SHA-256, and refuses to "load"
the model if the hash doesn't match the signed manifest from the control plane.

## Adding a real artifact

Replace the stub tarball with the real file, then update the `.sha256` sidecar:

```bash
sha256sum your-model.tar.gz | awk '{print $1}' > your-model.tar.gz.sha256
```

Then hit the `/api/artifacts` endpoint — it auto-discovers any `*.tar.gz` in this directory.
