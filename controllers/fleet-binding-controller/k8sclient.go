package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCACert    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

type KubeClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewKubeClient() (*KubeClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	if host == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST is not set")
	}
	port := getEnv("KUBERNETES_SERVICE_PORT", "443")

	tokenBytes, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, fmt.Errorf("reading service account token: %w", err)
	}

	caCert, err := os.ReadFile(serviceAccountCACert)
	if err != nil {
		return nil, fmt.Errorf("reading service account CA certificate: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse service account CA certificate")
	}

	return &KubeClient{
		baseURL: fmt.Sprintf("https://%s:%s", host, port),
		token:   strings.TrimSpace(string(tokenBytes)),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		},
	}, nil
}

// request issues an HTTP call against the API server. A 404 response is
// treated as "no content" rather than an error, matching how the reconciler
// probes for missing resources.
func (c *KubeClient) request(ctx context.Context, method, path string, body interface{}, contentType string, out interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed with HTTP %d: %s", method, path, resp.StatusCode, string(payload))
	}
	if len(payload) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(payload, out)
}

func (c *KubeClient) ListClusters(ctx context.Context) ([]Cluster, error) {
	path := fmt.Sprintf("/apis/%s/%s/%s", apiGroup, apiVersion, clustersResource)
	var list struct {
		Items []Cluster `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *KubeClient) ListVirtualClusterInstances(ctx context.Context, namespace string) ([]VirtualClusterInstance, error) {
	path := fmt.Sprintf("/apis/%s/%s/%s", apiGroup, apiVersion, virtualClustersResource)
	if namespace != "" {
		path = fmt.Sprintf(
			"/apis/%s/%s/namespaces/%s/%s",
			apiGroup,
			apiVersion,
			url.PathEscape(namespace),
			virtualClustersResource,
		)
	}
	var list struct {
		Items []VirtualClusterInstance `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *KubeClient) ListFleetProfiles(ctx context.Context, namespace string) ([]FleetProfile, error) {
	path := fmt.Sprintf(
		"/apis/%s/%s/namespaces/%s/%s",
		fleetAPIGroup,
		fleetAPIVersion,
		url.PathEscape(namespace),
		fleetProfilesResource,
	)
	var list struct {
		Items []FleetProfile `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *KubeClient) ListArgoCDApplications(ctx context.Context, namespace string) ([]Application, error) {
	path := fmt.Sprintf("/apis/%s/%s/%s", apiGroup, apiVersion, argoCDApplicationsResource)
	if namespace != "" {
		path = fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", apiGroup, apiVersion, url.PathEscape(namespace), argoCDApplicationsResource)
	}
	var list struct {
		Items []Application `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *KubeClient) CreateArgoCDApplication(ctx context.Context, namespace string, application Application) error {
	path := fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", apiGroup, apiVersion, url.PathEscape(namespace), argoCDApplicationsResource)
	return c.request(ctx, http.MethodPost, path, application, "application/json", nil)
}

func (c *KubeClient) PatchArgoCDApplication(ctx context.Context, namespace, name string, application Application) error {
	path := fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s/%s", apiGroup, apiVersion, url.PathEscape(namespace), argoCDApplicationsResource, url.PathEscape(name))
	// Do not include apiVersion or kind in a merge patch. Platform serves this
	// resource through management.loft.sh/v1 but stores it as storage.loft.sh/v1;
	// sending the served apiVersion in the patch body fails validation.
	patch := struct {
		Metadata struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Destination struct {
				// No omitempty: merge-patching null explicitly clears the
				// mutually-exclusive destination left by the other source kind.
				Cluster        *ClusterRef        `json:"cluster"`
				VirtualCluster *VirtualClusterRef `json:"virtualCluster"`
			} `json:"destination"`
			TemplateRef TemplateRef `json:"templateRef"`
			// Parameters intentionally has no omitempty. A JSON null in this
			// merge patch removes parameters when their Cluster annotations are
			// deleted instead of leaving stale per-binding overrides behind.
			Parameters map[string]interface{} `json:"parameters"`
		} `json:"spec"`
	}{}
	patch.Spec.Destination.Cluster = application.Spec.Destination.Cluster
	patch.Spec.Destination.VirtualCluster = application.Spec.Destination.VirtualCluster
	patch.Spec.TemplateRef = application.Spec.TemplateRef
	patch.Spec.Parameters = application.Spec.Parameters
	patch.Metadata.Labels = application.Metadata.Labels
	patch.Metadata.Annotations = application.Metadata.Annotations

	return c.request(ctx, http.MethodPatch, path, patch, "application/merge-patch+json", nil)
}

const (
	storageAPIGroup         = "storage.loft.sh"
	storageAPIVersion       = "v1"
	accessKeysResource      = "accesskeys"
	virtualClustersResource = "virtualclusterinstances"
)

func (c *KubeClient) ListAccessKeys(ctx context.Context) ([]AccessKey, error) {
	path := fmt.Sprintf("/apis/%s/%s/%s", storageAPIGroup, storageAPIVersion, accessKeysResource)
	selector := generatedByLabel + "=" + managedBy + "," + credentialPurposeLabel + "=" + writerPurpose
	path += "?labelSelector=" + url.QueryEscape(selector)
	var list struct {
		Items []AccessKey `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, "", &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *KubeClient) GetAccessKey(ctx context.Context, name string) (*AccessKey, error) {
	path := fmt.Sprintf("/apis/%s/%s/%s/%s", storageAPIGroup, storageAPIVersion, accessKeysResource, url.PathEscape(name))
	key := &AccessKey{}
	if err := c.request(ctx, http.MethodGet, path, nil, "", key); err != nil {
		return nil, err
	}
	if key.Metadata.Name == "" {
		return nil, nil
	}
	return key, nil
}

func (c *KubeClient) CreateAccessKey(ctx context.Context, key AccessKey) error {
	path := fmt.Sprintf("/apis/%s/%s/%s", storageAPIGroup, storageAPIVersion, accessKeysResource)
	err := c.request(ctx, http.MethodPost, path, key, "application/json", nil)
	if err != nil && key.Spec.Key != "" {
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), key.Spec.Key, "<redacted>"))
	}
	return err
}

func (c *KubeClient) DeleteAccessKey(ctx context.Context, name string) error {
	path := fmt.Sprintf("/apis/%s/%s/%s/%s", storageAPIGroup, storageAPIVersion, accessKeysResource, url.PathEscape(name))
	return c.request(ctx, http.MethodDelete, path, nil, "", nil)
}

func (c *KubeClient) DeleteArgoCDApplication(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s/%s", apiGroup, apiVersion, url.PathEscape(namespace), argoCDApplicationsResource, url.PathEscape(name))
	return c.request(ctx, http.MethodDelete, path, nil, "", nil)
}
