package pkgupdates

import (
	"context"

	"github.com/bitacora-dev/bitacora/internal/collector/docker"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// dockerItems compares each locally present image's digest against its
// registry's current one, via docker-socket-proxy for the local list
// (never the Docker socket directly, ADR-0005) and the standard OCI
// distribution API for the remote digest (registry.go). Only images with
// both a real tag and a locally recorded digest are checked — nothing
// to compare a dangling or never-pulled image against.
func dockerItems(ctx context.Context, metadataURL string, reg *registryClient) []schema.InventoryItem {
	if metadataURL == "" {
		return nil // no docker-socket-proxy configured — degrade silently
	}

	images, err := docker.NewMetadataClient(metadataURL).Images(ctx)
	if err != nil {
		return nil
	}

	var items []schema.InventoryItem
	for _, img := range images {
		ref, ok := parseImageReference(img.RepoTag)
		if !ok {
			continue
		}

		remoteDigest, ok := reg.Digest(ctx, ref)
		if !ok || remoteDigest == img.RepoDigest {
			continue
		}

		items = append(items, schema.InventoryItem{
			ID:   "docker_image:" + img.RepoTag,
			Name: img.RepoTag,
			Attrs: schema.Labels{
				"source":          "docker_image",
				"current_digest":  img.RepoDigest,
				"registry_digest": remoteDigest,
			},
		})
	}
	return items
}
