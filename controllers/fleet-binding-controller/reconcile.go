package main

import (
	"context"
	"strconv"
)

func reconcileOnce(ctx context.Context, client *KubeClient, cfg *Config) error {
	namespace := cfg.ProjectNamespace

	profileItems, err := client.ListFleetProfiles(ctx, namespace)
	if err != nil {
		return err
	}
	profiles := indexFleetProfiles(profileItems)

	clusters, err := client.ListClusters(ctx)
	if err != nil {
		return err
	}

	existingItems, err := client.ListArgoCDApplications(ctx, namespace)
	if err != nil {
		return err
	}
	existing := map[string]Application{}
	for _, item := range existingItems {
		if item.Metadata.Name != "" {
			existing[item.Metadata.Name] = item
		}
	}

	desired := map[string]Application{}
	protected := map[string]struct{}{}
	for _, cluster := range clusters {
		applications, err := desiredBindingsForCluster(cluster, cfg, profiles, existing)
		if err != nil {
			logError("cluster %s has invalid FleetProfile configuration: %v; preserving its existing bindings", cluster.Metadata.Name, err)
			for name, application := range existing {
				if application.Metadata.Labels[generatedByLabel] == managedBy &&
					application.Spec.Destination.Cluster.Name == cluster.Metadata.Name {
					protected[name] = struct{}{}
				}
			}
			continue
		}
		for _, application := range applications {
			desired[application.Metadata.Name] = application
		}
	}

	for name, application := range desired {
		if _, ok := existing[name]; ok {
			if err := client.PatchArgoCDApplication(ctx, namespace, name, application); err != nil {
				return err
			}
			logInfo("patched ArgoCDApplication %s/%s", namespace, name)
		} else {
			if err := client.CreateArgoCDApplication(ctx, namespace, application); err != nil {
				return err
			}
			logInfo("created ArgoCDApplication %s/%s", namespace, name)
		}
	}

	for _, application := range staleBindingsAtHighestDepth(existing, desired, protected) {
		name := application.Metadata.Name
		if err := client.DeleteArgoCDApplication(ctx, namespace, name); err != nil {
			return err
		}
		logInfo("deleted stale ArgoCDApplication %s/%s", namespace, name)
	}

	logInfo(
		"reconciled %d desired bindings from %d clusters and %d FleetProfiles in namespace %s",
		len(desired),
		len(clusters),
		len(profiles),
		namespace,
	)
	return nil
}

func staleBindingsAtHighestDepth(
	existing map[string]Application,
	desired map[string]Application,
	protected map[string]struct{},
) []Application {
	staleByDepth := map[int][]Application{}
	maxDepth := -1
	for name, application := range existing {
		if application.Metadata.Labels[generatedByLabel] != managedBy {
			continue
		}
		if _, ok := desired[name]; ok {
			continue
		}
		if _, ok := protected[name]; ok {
			continue
		}
		depth, err := strconv.Atoi(application.Metadata.Annotations[dependencyDepthAnnotation])
		if err != nil || depth < 0 {
			depth = 0
		}
		staleByDepth[depth] = append(staleByDepth[depth], application)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	// Prune one dependency depth per reconciliation. This lets Platform finish
	// deleting dependents before their prerequisites are removed.
	return staleByDepth[maxDepth]
}
