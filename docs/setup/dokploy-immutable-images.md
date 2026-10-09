# Deploying immutable development images with Dokploy

Each merge into `development` publishes `ghcr.io/bitacora-dev/bitacora-hub` with the full Git commit SHA and deploys that exact image to Dokploy. The service specification therefore changes on every deployment; Docker Swarm replaces the task instead of retaining the digest previously resolved for `:latest`.

## One-time setup

1. In Dokploy, create or update the development application with source type **Docker** and configure GHCR as its registry. The application must use `ghcr.io/bitacora-dev/bitacora-hub`.
2. Create a Dokploy API key with permission to update and deploy that application.
3. Add these repository Action secrets:

   | Secret | Value |
   | --- | --- |
   | `DOKPLOY_URL` | Dokploy base URL, without a trailing slash |
   | `DOKPLOY_API_KEY` | API key created in Dokploy |
   | `DOKPLOY_APPLICATION_ID` | Development application ID from Dokploy |
   | `DOKPLOY_REGISTRY_USERNAME` | GHCR username with pull access |
   | `DOKPLOY_REGISTRY_PASSWORD` | GHCR token with pull access |

4. Make the GHCR package public, or grant the configured GHCR credential pull access. For a multi-node Swarm, confirm the Dokploy deployment shares registry authentication with nodes.

The workflow first runs the same build, vet, test, read-only execution, and license-boundary checks as CI. It then publishes both the immutable SHA tag and `latest`, changes Dokploy's Docker image to the SHA tag through `application.saveDockerProvider`, and calls `application.deploy`.

## Verify a deployment

After merging a change to `development`, wait for the `Publish development image` workflow and its Dokploy deployment to finish. On the deployment server, confirm both the service image and the served frontend asset:

```sh
docker service ls --format '{{.Name}}\t{{.Image}}' | grep bitacora
curl -sSk -H 'Host: <development-host>' https://127.0.0.1/ \
  | grep -oE 'index-[A-Za-z0-9_-]+\.js'
```

The service image must end in the merge commit SHA, not `:latest`. Compare the asset fingerprint with `internal/webui/dist/index.html` at that same commit. This verifies the deployed binary serves the frontend built into the selected image without using Dokploy's `Stop` action.

## Roll back

Choose a previously published full commit SHA and update the Dokploy application's Docker image to `ghcr.io/bitacora-dev/bitacora-hub:<sha>`, then deploy. Because each SHA is immutable, the selected service revision is auditable and reproducible.

## Deliberately out of scope

This repository does not store Dokploy credentials, application IDs, or deployment hostnames. They belong in Dokploy and GitHub Actions secrets. The first real merge after the one-time setup must be verified on the deployment server using the commands above.
