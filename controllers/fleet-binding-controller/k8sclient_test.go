package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testHTTPClient(
	t *testing.T,
	body string,
	validate func(*http.Request),
) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			validate(request)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

func TestListFleetProfilesUsesProjectNamespace(t *testing.T) {
	responseBody := `{
			"items": [{
				"apiVersion": "fleet.lab.kurtmadel.com/v1alpha1",
				"kind": "FleetProfile",
				"metadata": {"name": "baseline", "namespace": "p-platform"},
				"spec": {
					"applications": [
						{"name": "cert-manager"},
						{"name": "cert-config", "dependsOn": ["cert-manager"]}
					]
				}
			}]
		}`

	client := &KubeClient{
		baseURL: "https://kubernetes.example",
		token:   "test-token",
		httpClient: testHTTPClient(t, responseBody, func(request *http.Request) {
			wantPath := "/apis/fleet.lab.kurtmadel.com/v1alpha1/namespaces/p-platform/fleetprofiles"
			if request.URL.Path != wantPath {
				t.Errorf("path %q, want %q", request.URL.Path, wantPath)
			}
			if request.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("unexpected Authorization header %q", request.Header.Get("Authorization"))
			}
		}),
	}
	profiles, err := client.ListFleetProfiles(context.Background(), "p-platform")
	if err != nil {
		t.Fatalf("list FleetProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Metadata.Name != "baseline" {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}
	applications := profiles[0].Spec.Applications
	if len(applications) != 2 || len(applications[1].DependsOn) != 1 {
		t.Fatalf("unexpected profile applications: %#v", applications)
	}
}

func TestListVirtualClusterInstancesCanBeClusterWide(t *testing.T) {
	client := &KubeClient{
		baseURL: "https://kubernetes.example",
		httpClient: testHTTPClient(t, `{"items":[{"metadata":{"name":"tenant-a","namespace":"p-team-a"}}]}`, func(request *http.Request) {
			wantPath := "/apis/management.loft.sh/v1/virtualclusterinstances"
			if request.URL.Path != wantPath {
				t.Errorf("path %q, want %q", request.URL.Path, wantPath)
			}
		}),
	}
	instances, err := client.ListVirtualClusterInstances(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Metadata.Namespace != "p-team-a" {
		t.Fatalf("unexpected instances: %#v", instances)
	}
}

func TestListArgoCDApplicationsReadsUnderlyingHealthAndSyncStatus(t *testing.T) {
	responseBody := `{
			"items": [{
				"metadata": {"name": "cert-manager-edge", "namespace": "p-platform"},
				"spec": {
					"destination": {"cluster": {"name": "edge"}},
					"templateRef": {"name": "cert-manager"}
				},
				"status": {
					"application": {
						"health": {"status": "Healthy"},
						"sync": {"status": "Synced"}
					}
				}
			}]
		}`

	client := &KubeClient{
		baseURL: "https://kubernetes.example",
		httpClient: testHTTPClient(t, responseBody, func(request *http.Request) {
			wantPath := "/apis/management.loft.sh/v1/namespaces/p-platform/argocdapplications"
			if request.URL.Path != wantPath {
				t.Errorf("path %q, want %q", request.URL.Path, wantPath)
			}
		}),
	}
	applications, err := client.ListArgoCDApplications(context.Background(), "p-platform")
	if err != nil {
		t.Fatalf("list ArgoCDApplications: %v", err)
	}
	if len(applications) != 1 || !applicationReady(applications[0]) {
		t.Fatalf("application status was not ready: %#v", applications)
	}
}

func TestPatchArgoCDApplicationOmitsTypeMetadata(t *testing.T) {
	client := &KubeClient{
		baseURL: "https://kubernetes.example",
		token:   "test-token",
		httpClient: testHTTPClient(t, `{}`, func(request *http.Request) {
			if request.Method != http.MethodPatch {
				t.Errorf("method %q, want PATCH", request.Method)
			}
			wantPath := "/apis/management.loft.sh/v1/namespaces/p-platform/argocdapplications/external-dns-edge"
			if request.URL.Path != wantPath {
				t.Errorf("path %q, want %q", request.URL.Path, wantPath)
			}
			if got := request.Header.Get("Content-Type"); got != "application/merge-patch+json" {
				t.Errorf("Content-Type %q, want application/merge-patch+json", got)
			}

			var patch map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			for _, field := range []string{"apiVersion", "kind", "status"} {
				if _, found := patch[field]; found {
					t.Errorf("patch unexpectedly contains %q: %#v", field, patch[field])
				}
			}
			if _, found := patch["metadata"]; !found {
				t.Errorf("patch is missing metadata: %#v", patch)
			}
			if _, found := patch["spec"]; !found {
				t.Errorf("patch is missing spec: %#v", patch)
			}
			spec, ok := patch["spec"].(map[string]interface{})
			if !ok {
				t.Fatalf("patch spec has unexpected type: %#v", patch["spec"])
			}
			parameters, found := spec["parameters"]
			if !found {
				t.Errorf("patch spec is missing parameters: %#v", spec)
			}
			if parameters != nil {
				t.Errorf("parameters = %#v, want null to clear stale values", parameters)
			}
			destination := spec["destination"].(map[string]interface{})
			if _, found := destination["virtualCluster"]; !found || destination["virtualCluster"] != nil {
				t.Errorf("virtualCluster = %#v, want explicit null", destination["virtualCluster"])
			}
		}),
	}

	application := Application{
		APIVersion: "management.loft.sh/v1",
		Kind:       "ArgoCDApplication",
		Metadata: ApplicationMeta{
			Name:      "external-dns-edge",
			Namespace: "p-platform",
			Labels: map[string]string{
				generatedByLabel: managedBy,
			},
			Annotations: map[string]string{
				dependsOnAnnotation: "envoy-gateway-config",
			},
		},
		Spec: ApplicationSpec{
			Destination: Destination{Cluster: &ClusterRef{Name: "edge"}},
			TemplateRef: TemplateRef{Name: "external-dns"},
		},
		Status: &ApplicationStatus{},
	}

	if err := client.PatchArgoCDApplication(
		context.Background(),
		"p-platform",
		"external-dns-edge",
		application,
	); err != nil {
		t.Fatalf("patch ArgoCDApplication: %v", err)
	}
}
