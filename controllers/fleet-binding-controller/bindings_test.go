package main

import (
	"reflect"
	"testing"
)

var testConfig = &Config{
	ProjectNamespace: "p-platform",
	ClusterSelector: Selector{
		MatchLabels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
	},
	DefaultProfiles:     []string{"control-plane-baseline"},
	ProfileAnnotation:   "fleet.lab.kurtmadel.com/profiles",
	ExtraAppsAnnotation: "fleet.lab.kurtmadel.com/extra-apps",
	SkipAppsAnnotation:  "fleet.lab.kurtmadel.com/skip-apps",
}

var testProfiles = indexFleetProfiles([]FleetProfile{
	testProfile("control-plane-baseline", application("cert-manager"), application("metrics-server"), application("metallb")),
	testProfile("vcp-management-cluster-baseline", application("cert-manager"), application("metrics-server"), application("vcluster-gitops-watcher")),
	testProfile("gpu-nvidia-baseline", application("nvidia-gpu-operator"), application("nvidia-dra-driver-gpu")),
})

func application(name string, dependencies ...string) FleetProfileApplication {
	return FleetProfileApplication{Name: name, DependsOn: dependencies}
}

func testProfile(name string, applications ...FleetProfileApplication) FleetProfile {
	return FleetProfile{
		Metadata: ObjectMeta{Name: name},
		Spec:     FleetProfileSpec{Applications: applications},
	}
}

func testCluster(name string, labels, annotations map[string]string, argoCDEnabled bool) Cluster {
	return Cluster{
		Metadata: ObjectMeta{Name: name, Labels: labels, Annotations: annotations},
		Spec:     ClusterSpec{ArgoCD: ArgoCDSpec{Enabled: argoCDEnabled}},
	}
}

func templateNames(bindings []Application) []string {
	names := make([]string, len(bindings))
	for i, binding := range bindings {
		names[i] = binding.Spec.TemplateRef.Name
	}
	return names
}

func desiredTemplateNames(
	t *testing.T,
	cluster Cluster,
	profiles map[string]FleetProfile,
	existing map[string]Application,
) []string {
	t.Helper()
	bindings, err := desiredBindingsForCluster(cluster, testConfig, profiles, existing)
	if err != nil {
		t.Fatalf("desired bindings: %v", err)
	}
	return templateNames(bindings)
}

func TestDefaultProfileIsUsedForSelectedCluster(t *testing.T) {
	cluster := testCluster(
		"cp-blacksburg-dc1",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		true,
	)

	got := desiredTemplateNames(t, cluster, testProfiles, nil)
	want := []string{"cert-manager", "metrics-server", "metallb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestProfileAnnotationCanCombineProfiles(t *testing.T) {
	cluster := testCluster(
		"gpu-cluster",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/profiles": "control-plane-baseline,gpu-nvidia-baseline",
		},
		true,
	)

	got := desiredTemplateNames(t, cluster, testProfiles, nil)
	want := []string{
		"cert-manager", "metrics-server", "metallb",
		"nvidia-gpu-operator", "nvidia-dra-driver-gpu",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtraAndSkipAnnotationsAdjustApps(t *testing.T) {
	cluster := testCluster(
		"edge",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/extra-apps": "custom-app",
			"fleet.lab.kurtmadel.com/skip-apps":  "metallb",
		},
		true,
	)

	got := desiredTemplateNames(t, cluster, testProfiles, nil)
	want := []string{"cert-manager", "metrics-server", "custom-app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTemplateParameterAnnotationsBecomeConcreteApplicationParameters(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile(
			"fleet-observability-platform",
			application("fleet-observability-prometheus"),
			application("fleet-observability-grafana"),
		),
	})
	cluster := testCluster(
		"loft-cluster",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/profiles": "fleet-observability-platform",
			"fleet-observability-grafana.argocd-template-param.fleet.lab.kurtmadel.com/platformHost":      "vcp.lab.kurtmadel.com",
			"fleet-observability-grafana.argocd-template-param.fleet.lab.kurtmadel.com/grafanaAdminGroup": "platform-admins",
			"unselected-template.argocd-template-param.fleet.lab.kurtmadel.com/ignored":                    "value",
		},
		true,
	)

	bindings, err := desiredBindingsForCluster(cluster, testConfig, profiles, nil)
	if err != nil {
		t.Fatalf("desired bindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("got %d bindings, want 2", len(bindings))
	}
	if bindings[0].Spec.Parameters != nil {
		t.Fatalf("Prometheus parameters = %#v, want nil", bindings[0].Spec.Parameters)
	}
	want := map[string]interface{}{
		"platformHost":      "vcp.lab.kurtmadel.com",
		"grafanaAdminGroup": "platform-admins",
	}
	if !reflect.DeepEqual(bindings[1].Spec.Parameters, want) {
		t.Fatalf("Grafana parameters = %#v, want %#v", bindings[1].Spec.Parameters, want)
	}
}

func TestTemplateParameterAnnotationKeepsExactNameAndEmptyValue(t *testing.T) {
	annotations := map[string]string{
		"example.argocd-template-param.fleet.lab.kurtmadel.com/camelCase_name": "",
		"example.argocd-template-param.fleet.lab.kurtmadel.com/other":          "set",
		"example.argocd-template-param.fleet.lab.kurtmadel.com":                "ignored",
	}

	want := map[string]interface{}{"camelCase_name": "", "other": "set"}
	if got := parametersForTemplate(annotations, "example"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestUnselectedClusterGetsNoBindings(t *testing.T) {
	cluster := testCluster("other", nil, nil, true)

	bindings, err := desiredBindingsForCluster(cluster, testConfig, testProfiles, nil)
	if err != nil {
		t.Fatalf("desired bindings: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings, got %v", bindings)
	}
}

func TestClusterWithArgoCDDisabledGetsNoBindings(t *testing.T) {
	cluster := testCluster(
		"cp-blacksburg-dc1",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		false,
	)

	bindings, err := desiredBindingsForCluster(cluster, testConfig, testProfiles, nil)
	if err != nil {
		t.Fatalf("desired bindings: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings when spec.argoCD.enabled is false, got %v", bindings)
	}
}

func TestClusterWithoutArgoCDSpecGetsNoBindings(t *testing.T) {
	cluster := Cluster{
		Metadata: ObjectMeta{
			Name:   "cp-blacksburg-dc1",
			Labels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		},
	}

	bindings, err := desiredBindingsForCluster(cluster, testConfig, testProfiles, nil)
	if err != nil {
		t.Fatalf("desired bindings: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings when spec.argoCD is unset, got %v", bindings)
	}
}

func TestDependentBindingWaitsForHealthySyncedPrerequisite(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile(
			"control-plane-baseline",
			application("cert-manager"),
			application("cert-config", "cert-manager"),
		),
	})
	cluster := testCluster(
		"edge",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		true,
	)

	got := desiredTemplateNames(t, cluster, profiles, nil)
	if want := []string{"cert-manager"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("without prerequisite status got %v, want %v", got, want)
	}

	existing := map[string]Application{
		bindingName("cert-manager", "edge"): existingBinding("cert-manager", "edge", "Healthy", "Synced"),
	}
	got = desiredTemplateNames(t, cluster, profiles, existing)
	if want := []string{"cert-manager", "cert-config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("with ready prerequisite got %v, want %v", got, want)
	}
}

func TestHealthyButOutOfSyncPrerequisiteBlocksDependent(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile(
			"control-plane-baseline",
			application("cert-manager"),
			application("cert-config", "cert-manager"),
		),
	})
	cluster := testCluster(
		"edge",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		true,
	)
	existing := map[string]Application{
		bindingName("cert-manager", "edge"): existingBinding("cert-manager", "edge", "Healthy", "OutOfSync"),
	}

	got := desiredTemplateNames(t, cluster, profiles, existing)
	if want := []string{"cert-manager"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExistingDependentIsPreservedWhenPrerequisiteDegrades(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile(
			"control-plane-baseline",
			application("cert-manager"),
			application("cert-config", "cert-manager"),
		),
	})
	cluster := testCluster(
		"edge",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		true,
	)
	existing := map[string]Application{
		bindingName("cert-manager", "edge"): existingBinding("cert-manager", "edge", "Degraded", "Synced"),
		bindingName("cert-config", "edge"):  existingBinding("cert-config", "edge", "Healthy", "Synced"),
	}

	got := desiredTemplateNames(t, cluster, profiles, existing)
	if want := []string{"cert-manager", "cert-config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplicationDependencyCycleIsRejected(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile(
			"control-plane-baseline",
			application("cert-manager", "cert-config"),
			application("cert-config", "cert-manager"),
		),
	})

	_, err := resolveApplications([]string{"control-plane-baseline"}, profiles, nil, nil)
	if err == nil {
		t.Fatal("expected dependency cycle error")
	}
}

func TestMissingApplicationDependencyIsRejected(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile("control-plane-baseline", application("cert-config", "cert-manager")),
	})

	_, err := resolveApplications([]string{"control-plane-baseline"}, profiles, nil, nil)
	if err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestStaleBindingsPruneHighestDependencyDepthFirst(t *testing.T) {
	root := existingBinding("cert-manager", "edge", "Healthy", "Synced")
	root.Metadata.Annotations = map[string]string{dependencyDepthAnnotation: "0"}
	dependent := existingBinding("cert-config", "edge", "Healthy", "Synced")
	dependent.Metadata.Annotations = map[string]string{dependencyDepthAnnotation: "1"}
	existing := map[string]Application{
		root.Metadata.Name:      root,
		dependent.Metadata.Name: dependent,
	}

	stale := staleBindingsAtHighestDepth(existing, nil, nil)
	if got, want := templateNames(stale), []string{"cert-config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func existingBinding(template, cluster, health, sync string) Application {
	return Application{
		Metadata: ApplicationMeta{
			Name: bindingName(template, cluster),
			Labels: map[string]string{
				generatedByLabel: managedBy,
			},
		},
		Spec: ApplicationSpec{
			Destination: Destination{Cluster: ClusterRef{Name: cluster}},
			TemplateRef: TemplateRef{Name: template},
		},
		Status: &ApplicationStatus{
			Application: &ArgoApplicationStatus{
				Health: ArgoHealthStatus{Status: health},
				Sync:   ArgoSyncStatus{Status: sync},
			},
		},
	}
}
