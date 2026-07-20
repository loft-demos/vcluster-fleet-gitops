package main

import (
	"reflect"
	"strings"
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
	VCIObservability: VCIObservabilityConfig{
		Enabled:                   true,
		PrivateNodesProfile:       "tenant-observability-private-nodes",
		SharedNodesProfile:        "tenant-observability-shared-nodes",
		OTLPEndpoint:              "https://otel.lab.kurtmadel.com",
		DisabledAnnotation:        "fleet.lab.kurtmadel.com/observability-disabled",
		ProfileOverrideAnnotation: "fleet.lab.kurtmadel.com/observability-profile",
	},
}

var testProfiles = indexFleetProfiles([]FleetProfile{
	testProfile("tenant-observability-private-nodes", application("cluster-collector")),
	testProfile("tenant-observability-shared-nodes", application("shared-node-tenant-collector")),
	testProfile("control-plane-baseline", application("cert-manager"), application("metrics-server"), application("metallb")),
	testProfile("vcp-management-cluster-baseline", application("cert-manager"), application("metrics-server"), application("vcluster-gitops-watcher")),
	testProfile("gpu-nvidia-baseline", application("nvidia-gpu-operator"), application("nvidia-dra-driver-gpu")),
})

func testVirtualClusterInstance(namespace, name, values string, annotations map[string]string) VirtualClusterInstance {
	instance := VirtualClusterInstance{
		Metadata: ObjectMeta{Namespace: namespace, Name: name, Annotations: annotations},
	}
	if values != "" {
		instance.Status.VirtualCluster = &VirtualClusterTemplateDefinition{
			HelmRelease: VirtualClusterHelmRelease{Values: values},
		}
	}
	return instance
}

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
			"example-profile",
			application("example-backend"),
			application("example-dashboard"),
		),
	})
	cluster := testCluster(
		"loft-cluster",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/profiles":                                             "example-profile",
			"example-dashboard.argocd-template-param.fleet.lab.kurtmadel.com/externalHost": "dashboard.example.com",
			"example-dashboard.argocd-template-param.fleet.lab.kurtmadel.com/adminGroup":   "platform-admins",
			"unselected-template.argocd-template-param.fleet.lab.kurtmadel.com/ignored":    "value",
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
		t.Fatalf("backend parameters = %#v, want nil", bindings[0].Spec.Parameters)
	}
	want := map[string]interface{}{
		"externalHost": "dashboard.example.com",
		"adminGroup":   "platform-admins",
	}
	if !reflect.DeepEqual(bindings[1].Spec.Parameters, want) {
		t.Fatalf("dashboard parameters = %#v, want %#v", bindings[1].Spec.Parameters, want)
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

func TestVirtualClusterAutomaticallySelectsSharedNodesObservability(t *testing.T) {
	instance := testVirtualClusterInstance("p-default", "shared-tenant", "privateNodes:\n  enabled: false\n", nil)
	bindings, enrolled, ready, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, testProfiles, nil)
	if err != nil {
		t.Fatalf("desired VCI bindings: %v", err)
	}
	if !enrolled || !ready {
		t.Fatalf("enrolled=%v ready=%v, want true/true", enrolled, ready)
	}
	if got, want := templateNames(bindings), []string{"shared-node-tenant-collector"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := bindings[0].Metadata.Namespace; got != "p-default" {
		t.Fatalf("namespace %q, want p-default", got)
	}
	destination := bindings[0].Spec.Destination
	if destination.Cluster != nil || destination.VirtualCluster == nil ||
		destination.VirtualCluster.Name != "shared-tenant" || destination.VirtualCluster.Target != "vCluster" {
		t.Fatalf("unexpected destination: %#v", destination)
	}
}

func TestVirtualClusterAutomaticallySelectsPrivateNodesObservability(t *testing.T) {
	instance := testVirtualClusterInstance("p-team-a", "private-tenant", "privateNodes:\n  enabled: true\n  autoNodes:\n    - provider: demo\n", nil)
	bindings, enrolled, ready, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, testProfiles, nil)
	if err != nil || !enrolled || !ready {
		t.Fatalf("bindings failed: enrolled=%v ready=%v err=%v", enrolled, ready, err)
	}
	if got, want := templateNames(bindings), []string{"cluster-collector"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if bindings[0].Metadata.Name == bindingName("cluster-collector", "private-tenant") {
		t.Fatalf("VCI binding name collides with Cluster binding: %s", bindings[0].Metadata.Name)
	}
}

func TestVirtualClusterCollectorsReceiveDefaultOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		helmValues string
		template   string
	}{
		{name: "private nodes", helmValues: "privateNodes:\n  enabled: true\n", template: privateNodesCollectorTemplate},
		{name: "shared nodes", helmValues: "privateNodes:\n  enabled: false\n", template: sharedNodesCollectorTemplate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := testVirtualClusterInstance("p-default", "tenant", tt.helmValues, nil)
			bindings, _, _, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, testProfiles, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(bindings) != 1 || bindings[0].Spec.TemplateRef.Name != tt.template {
				t.Fatalf("unexpected bindings: %#v", bindings)
			}
			if got := bindings[0].Spec.Parameters[otlpEndpointParameter]; got != testConfig.VCIObservability.OTLPEndpoint {
				t.Fatalf("otlpEndpoint = %#v, want %q", got, testConfig.VCIObservability.OTLPEndpoint)
			}
		})
	}
}

func TestVirtualClusterCollectorAnnotationOverridesDefaultOTLPEndpoint(t *testing.T) {
	const override = "https://tenant-otel.example.com"
	instance := testVirtualClusterInstance("p-default", "tenant", "privateNodes:\n  enabled: true\n", map[string]string{
		privateNodesCollectorTemplate + templateParameterAnnotationSuffix + "/" + otlpEndpointParameter: override,
	})
	bindings, _, _, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, testProfiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := bindings[0].Spec.Parameters[otlpEndpointParameter]; got != override {
		t.Fatalf("otlpEndpoint = %#v, want annotation override %q", got, override)
	}
}

func TestVirtualClusterOTLPEndpointDefaultIsCollectorOnly(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile("tenant-observability-shared-nodes", application(sharedNodesCollectorTemplate)),
		testProfile("tenant-addon", application("tenant-dashboard")),
	})
	instance := testVirtualClusterInstance("p-default", "tenant", "privateNodes:\n  enabled: false\n", map[string]string{
		testConfig.ProfileAnnotation: "tenant-addon",
	})
	bindings, _, _, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("got %d bindings, want 2", len(bindings))
	}
	if bindings[0].Spec.Parameters[otlpEndpointParameter] != testConfig.VCIObservability.OTLPEndpoint {
		t.Fatalf("collector parameters = %#v", bindings[0].Spec.Parameters)
	}
	if bindings[1].Spec.Parameters != nil {
		t.Fatalf("non-collector parameters = %#v, want nil", bindings[1].Spec.Parameters)
	}
}

func TestVirtualClusterObservabilityOverrideAndAdditionalProfiles(t *testing.T) {
	profiles := indexFleetProfiles([]FleetProfile{
		testProfile("custom-observability", application("custom-collector")),
		testProfile("tenant-addon", application("tenant-dashboard")),
	})
	instance := testVirtualClusterInstance("p-default", "tenant", "privateNodes:\n  enabled: false\n", map[string]string{
		testConfig.VCIObservability.ProfileOverrideAnnotation: "custom-observability",
		testConfig.ProfileAnnotation:                          "tenant-addon",
	})
	bindings, _, _, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, profiles, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := templateNames(bindings), []string{"custom-collector", "tenant-dashboard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestVirtualClusterCanOptOutAndWaitsForRenderedValues(t *testing.T) {
	disabled := testVirtualClusterInstance("p-default", "disabled", "privateNodes:\n  enabled: false\n", map[string]string{
		testConfig.VCIObservability.DisabledAnnotation: "true",
	})
	bindings, enrolled, ready, err := desiredBindingsForVirtualClusterInstance(disabled, testConfig, testProfiles, nil)
	if err != nil || enrolled || ready || len(bindings) != 0 {
		t.Fatalf("disabled VCI got bindings=%v enrolled=%v ready=%v err=%v", bindings, enrolled, ready, err)
	}

	pending := testVirtualClusterInstance("p-default", "pending", "", nil)
	bindings, enrolled, ready, err = desiredBindingsForVirtualClusterInstance(pending, testConfig, testProfiles, nil)
	if err != nil || !enrolled || ready || len(bindings) != 0 {
		t.Fatalf("pending VCI got bindings=%v enrolled=%v ready=%v err=%v", bindings, enrolled, ready, err)
	}
}

func TestRenderedVirtualClusterWithEmptyValuesIsSharedNodes(t *testing.T) {
	instance := testVirtualClusterInstance("p-default", "empty-values", "placeholder", nil)
	instance.Status.VirtualCluster.HelmRelease.Values = ""
	bindings, enrolled, ready, err := desiredBindingsForVirtualClusterInstance(instance, testConfig, testProfiles, nil)
	if err != nil || !enrolled || !ready {
		t.Fatalf("enrolled=%v ready=%v err=%v", enrolled, ready, err)
	}
	if got, want := templateNames(bindings), []string{"shared-node-tenant-collector"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLongVirtualClusterBindingNamesDoNotCollide(t *testing.T) {
	a := virtualClusterBindingName("shared-node-tenant-collector", strings.Repeat("a", 70)+"x")
	b := virtualClusterBindingName("shared-node-tenant-collector", strings.Repeat("a", 70)+"y")
	if len(a) > 63 || len(b) > 63 || a == b {
		t.Fatalf("VCI binding names are not collision safe: %q %q", a, b)
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
			Destination: Destination{Cluster: &ClusterRef{Name: cluster}},
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
