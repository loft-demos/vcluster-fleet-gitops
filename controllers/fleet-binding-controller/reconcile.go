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

	var virtualClusters []VirtualClusterInstance
	if cfg.VCIObservability.Enabled {
		virtualClusters, err = client.ListVirtualClusterInstances(ctx, "")
		if err != nil {
			return err
		}
	}

	applicationNamespace := namespace
	if cfg.VCIObservability.Enabled {
		applicationNamespace = ""
	}
	existingItems, err := client.ListArgoCDApplications(ctx, applicationNamespace)
	if err != nil {
		return err
	}
	existing := map[string]Application{}
	for _, item := range existingItems {
		if item.Metadata.Name != "" {
			existing[applicationKey(item.Metadata.Namespace, item.Metadata.Name)] = item
		}
	}

	desired := map[string]Application{}
	protected := map[string]struct{}{}
	for _, cluster := range clusters {
		applications, err := desiredBindingsForCluster(cluster, cfg, profiles, applicationsInNamespace(existing, namespace))
		if err != nil {
			logError("cluster %s has invalid FleetProfile configuration: %v; preserving its existing bindings", cluster.Metadata.Name, err)
			for name, application := range existing {
				if application.Metadata.Labels[generatedByLabel] == managedBy &&
					application.Spec.Destination.Cluster != nil &&
					application.Spec.Destination.Cluster.Name == cluster.Metadata.Name {
					protected[name] = struct{}{}
				}
			}
			continue
		}
		for _, application := range applications {
			desired[applicationKey(application.Metadata.Namespace, application.Metadata.Name)] = application
		}
	}

	credentialDesired := map[string]struct{}{}
	for _, instance := range virtualClusters {
		instanceExisting := applicationsInNamespace(existing, instance.Metadata.Namespace)
		applications, enrolled, ready, err := desiredBindingsForVirtualClusterInstance(instance, cfg, profiles, instanceExisting)
		if !enrolled {
			if cfg.WriterCredentials.Enabled {
				if err := deleteWriterCredential(ctx, client, cfg, instance.Metadata.Namespace, instance.Metadata.Name, true); err != nil {
					logError("remove writer credential for opted-out VirtualClusterInstance %s/%s: %v", instance.Metadata.Namespace, instance.Metadata.Name, err)
					protectVirtualClusterBindings(existing, protected, instance.Metadata.Namespace, instance.Metadata.Name)
					credentialDesired[applicationKey(projectNameFromNamespace(instance.Metadata.Namespace), instance.Metadata.Name)] = struct{}{}
				}
			}
			continue
		}
		credentialDesired[applicationKey(projectNameFromNamespace(instance.Metadata.Namespace), instance.Metadata.Name)] = struct{}{}
		if err != nil {
			logError("VirtualClusterInstance %s has invalid rendered values or FleetProfile configuration: %v; preserving its existing bindings", instance.Metadata.Name, err)
			protectVirtualClusterBindings(existing, protected, instance.Metadata.Namespace, instance.Metadata.Name)
			continue
		}
		if !ready {
			logDebug("VirtualClusterInstance %s is waiting for rendered Helm values before observability classification", instance.Metadata.Name)
			protectVirtualClusterBindings(existing, protected, instance.Metadata.Namespace, instance.Metadata.Name)
			continue
		}
		if cfg.WriterCredentials.Enabled {
			if err := reconcileWriterCredential(ctx, client, cfg, instance.Metadata.Namespace, instance.Metadata.Name); err != nil {
				logError("VirtualClusterInstance %s writer credential reconciliation failed: %v; preserving its existing bindings", instance.Metadata.Name, err)
				protectVirtualClusterBindings(existing, protected, instance.Metadata.Namespace, instance.Metadata.Name)
				continue
			}
		}
		for _, application := range applications {
			desired[applicationKey(application.Metadata.Namespace, application.Metadata.Name)] = application
		}
	}

	if cfg.WriterCredentials.Enabled {
		keys, err := client.ListAccessKeys(ctx)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if key.Metadata.Labels[generatedByLabel] != managedBy || key.Metadata.Labels[credentialPurposeLabel] != writerPurpose {
				continue
			}
			instanceName := key.Metadata.Labels[virtualClusterLabel]
			projectName := key.Metadata.Labels["loft.sh/project"]
			if _, ok := credentialDesired[applicationKey(projectName, instanceName)]; ok {
				continue
			}
			if err := client.DeleteAccessKey(ctx, key.Metadata.Name); err != nil {
				return err
			}
			delete(writerSecretLastSync, applicationKey(projectName, instanceName))
			logInfo("deleted stale per-VCI metrics writer AccessKey %s", key.Metadata.Name)
		}
	}

	for key, application := range desired {
		name := application.Metadata.Name
		applicationNamespace := application.Metadata.Namespace
		if _, ok := existing[key]; ok {
			if err := client.PatchArgoCDApplication(ctx, applicationNamespace, name, application); err != nil {
				return err
			}
			logInfo("patched ArgoCDApplication %s/%s", applicationNamespace, name)
		} else {
			if err := client.CreateArgoCDApplication(ctx, applicationNamespace, application); err != nil {
				return err
			}
			logInfo("created ArgoCDApplication %s/%s", applicationNamespace, name)
		}
	}

	for _, application := range staleBindingsAtHighestDepth(existing, desired, protected) {
		name := application.Metadata.Name
		applicationNamespace := application.Metadata.Namespace
		if err := client.DeleteArgoCDApplication(ctx, applicationNamespace, name); err != nil {
			return err
		}
		logInfo("deleted stale ArgoCDApplication %s/%s", applicationNamespace, name)
	}

	logInfo(
		"reconciled %d desired bindings from %d clusters, %d VirtualClusterInstances, and %d FleetProfiles in namespace %s",
		len(desired),
		len(clusters),
		len(virtualClusters),
		len(profiles),
		namespace,
	)
	return nil
}

func applicationKey(namespace, name string) string {
	return namespace + "/" + name
}

func applicationsInNamespace(existing map[string]Application, namespace string) map[string]Application {
	result := map[string]Application{}
	for _, application := range existing {
		if application.Metadata.Namespace == namespace {
			result[application.Metadata.Name] = application
		}
	}
	return result
}

func protectVirtualClusterBindings(existing map[string]Application, protected map[string]struct{}, namespace, instance string) {
	for name, application := range existing {
		if application.Metadata.Labels[generatedByLabel] == managedBy &&
			application.Metadata.Namespace == namespace &&
			application.Spec.Destination.VirtualCluster != nil &&
			application.Spec.Destination.VirtualCluster.Name == instance {
			protected[name] = struct{}{}
		}
	}
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
