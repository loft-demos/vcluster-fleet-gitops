package main

import "context"

func reconcileOnce(ctx context.Context, client *KubeClient, cfg *Config) error {
	namespace := cfg.ProjectNamespace

	clusters, err := client.ListClusters(ctx)
	if err != nil {
		return err
	}

	desired := map[string]Application{}
	for _, cluster := range clusters {
		for _, application := range desiredBindingsForCluster(cluster, cfg) {
			desired[application.Metadata.Name] = application
		}
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

	for name, application := range existing {
		if application.Metadata.Labels[generatedByLabel] != managedBy {
			continue
		}
		if _, ok := desired[name]; ok {
			continue
		}
		if err := client.DeleteArgoCDApplication(ctx, namespace, name); err != nil {
			return err
		}
		logInfo("deleted stale ArgoCDApplication %s/%s", namespace, name)
	}

	logInfo("reconciled %d desired bindings from %d clusters in namespace %s", len(desired), len(clusters), namespace)
	return nil
}
