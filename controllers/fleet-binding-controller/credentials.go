package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
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
	credentialPurposeLabel = "fleet.lab.kurtmadel.com/credential-purpose"
	writerPurpose          = "metrics-writer"
	installerPurpose       = "tenant-secret-installer"
	metricsWriterGroup     = "loft:system:metrics-writers"
)

var writerSecretLastSync = map[string]time.Time{}

func projectNameFromNamespace(namespace string) string {
	if strings.HasPrefix(namespace, "loft-p-") {
		return strings.TrimPrefix(namespace, "loft-p-")
	}
	if strings.HasPrefix(namespace, "p-") {
		return strings.TrimPrefix(namespace, "p-")
	}
	return namespace
}

func credentialName(prefix, project, instance string) string {
	name := dnsLabel(strings.Join([]string{prefix, project, instance}, "-"))
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:54], "-.") + "-" + hex.EncodeToString(digest[:4])
}

func randomAccessKey() (string, error) {
	data := make([]byte, 48)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func accessKeyScope(project, instance string, roles ...string) *AccessKeyScope {
	scopeRoles := make([]AccessKeyScopeRole, 0, len(roles))
	for _, role := range roles {
		scopeRoles = append(scopeRoles, AccessKeyScopeRole{Role: role})
	}
	return &AccessKeyScope{
		Roles: scopeRoles,
		VirtualClusters: []AccessKeyScopeVirtualCluster{{
			Project:        project,
			VirtualCluster: instance,
		}},
	}
}

func accessKeyMatchesVCI(key *AccessKey, project, instance string) bool {
	return key != nil && key.Spec.Scope != nil && len(key.Spec.Scope.VirtualClusters) == 1 &&
		key.Spec.Scope.VirtualClusters[0].Project == project &&
		key.Spec.Scope.VirtualClusters[0].VirtualCluster == instance
}

func writerAccessKeyValid(key *AccessKey, project, instance string) bool {
	return accessKeyMatchesVCI(key, project, instance) &&
		key.Spec.Type == "Other" && key.Spec.Subject == "metrics-writer" && key.Spec.Key != "" &&
		len(key.Spec.Groups) == 1 && key.Spec.Groups[0] == metricsWriterGroup &&
		key.Spec.Scope != nil && len(key.Spec.Scope.Roles) == 1 && key.Spec.Scope.Roles[0].Role == "metrics-writer"
}

func desiredWriterAccessKey(projectNamespace, instance string) (AccessKey, error) {
	project := projectNameFromNamespace(projectNamespace)
	token, err := randomAccessKey()
	if err != nil {
		return AccessKey{}, fmt.Errorf("generate metrics writer token: %w", err)
	}
	name := credentialName("fleet-observability-writer", project, instance)
	return AccessKey{
		APIVersion: storageAPIGroup + "/" + storageAPIVersion,
		Kind:       "AccessKey",
		Metadata: ObjectMeta{
			Name: name,
			Labels: map[string]string{
				generatedByLabel:          managedBy,
				virtualClusterLabel:       instance,
				credentialPurposeLabel:    writerPurpose,
				"loft.sh/project":         project,
				"loft.sh/virtual-cluster": instance,
			},
		},
		Spec: AccessKeySpec{
			DisplayName: fmt.Sprintf("Fleet Observability writer for %s/%s", project, instance),
			Type:        "Other",
			Key:         token,
			Subject:     "metrics-writer",
			Groups:      []string{metricsWriterGroup},
			Scope:       accessKeyScope(project, instance, "metrics-writer"),
		},
	}, nil
}

func ensureWriterAccessKey(ctx context.Context, client *KubeClient, projectNamespace, instance string) (*AccessKey, error) {
	desired, err := desiredWriterAccessKey(projectNamespace, instance)
	if err != nil {
		return nil, err
	}
	existing, err := client.GetAccessKey(ctx, desired.Metadata.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		project := projectNameFromNamespace(projectNamespace)
		if existing.Metadata.Labels[generatedByLabel] != managedBy ||
			existing.Metadata.Labels[credentialPurposeLabel] != writerPurpose ||
			!writerAccessKeyValid(existing, project, instance) {
			return nil, fmt.Errorf("AccessKey %q exists but is not the expected controller-managed VCI writer", desired.Metadata.Name)
		}
		return existing, nil
	}
	if err := client.CreateAccessKey(ctx, desired); err != nil {
		return nil, err
	}
	delete(writerSecretLastSync, applicationKey(projectNameFromNamespace(projectNamespace), instance))
	logInfo("created per-VCI metrics writer AccessKey %s", desired.Metadata.Name)
	return &desired, nil
}

func createInstallerAccessKey(ctx context.Context, client *KubeClient, cfg *Config, projectNamespace, instance string) (*AccessKey, error) {
	project := projectNameFromNamespace(projectNamespace)
	name := credentialName("fleet-observability-installer", project, instance)
	existing, err := client.GetAccessKey(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Metadata.Labels[generatedByLabel] != managedBy ||
			existing.Metadata.Labels[credentialPurposeLabel] != installerPurpose ||
			!accessKeyMatchesVCI(existing, project, instance) || existing.Spec.Type != "User" ||
			existing.Spec.User != cfg.WriterCredentials.InstallerUser || len(existing.Spec.Scope.Roles) != 0 {
			return nil, fmt.Errorf("AccessKey %q exists but is not the expected controller-managed installer", name)
		}
		// A previous reconciliation may have exited before its deferred cleanup.
		// Replace that possibly expired five-minute credential with a fresh one.
		if err := client.DeleteAccessKey(ctx, name); err != nil {
			return nil, err
		}
	}
	token, err := randomAccessKey()
	if err != nil {
		return nil, fmt.Errorf("generate installer token: %w", err)
	}
	key := AccessKey{
		APIVersion: storageAPIGroup + "/" + storageAPIVersion,
		Kind:       "AccessKey",
		Metadata: ObjectMeta{
			Name: name,
			Labels: map[string]string{
				generatedByLabel:       managedBy,
				virtualClusterLabel:    instance,
				credentialPurposeLabel: installerPurpose,
			},
		},
		Spec: AccessKeySpec{
			DisplayName: fmt.Sprintf("Temporary Fleet Observability Secret installer for %s/%s", project, instance),
			Type:        "User",
			Key:         token,
			User:        cfg.WriterCredentials.InstallerUser,
			TTL:         300,
			Scope:       accessKeyScope(project, instance),
		},
	}
	if err := client.CreateAccessKey(ctx, key); err != nil {
		return nil, err
	}
	return &key, nil
}

type tenantClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newTenantClient(cfg *Config, projectNamespace, instance, token string) (*tenantClient, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if cfg.WriterCredentials.PlatformCAPath != "" {
		pem, err := os.ReadFile(cfg.WriterCredentials.PlatformCAPath)
		if err != nil {
			return nil, fmt.Errorf("read Platform CA: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("parse Platform CA from %s", cfg.WriterCredentials.PlatformCAPath)
		}
	}
	project := projectNameFromNamespace(projectNamespace)
	base := fmt.Sprintf(
		"%s/kubernetes/project/%s/virtualcluster/%s",
		cfg.WriterCredentials.PlatformURL,
		url.PathEscape(project),
		url.PathEscape(instance),
	)
	return &tenantClient{
		baseURL: base,
		token:   token,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				RootCAs:            pool,
				InsecureSkipVerify: cfg.WriterCredentials.InsecureSkipVerify, // #nosec G402 -- explicit demo-only configuration
			}},
		},
	}, nil
}

func (c *tenantClient) request(ctx context.Context, method, path string, body interface{}) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, payload, fmt.Errorf("%s %s failed with HTTP %d: %s", method, path, resp.StatusCode, string(payload))
	}
	return resp.StatusCode, payload, nil
}

func (c *tenantClient) ensureWriterSecret(ctx context.Context, namespace, name, writerToken string) error {
	namespacePath := "/api/v1/namespaces/" + url.PathEscape(namespace)
	status, _, err := c.request(ctx, http.MethodGet, namespacePath, nil)
	if err != nil && status != http.StatusNotFound {
		return err
	}
	if status == http.StatusNotFound {
		_, _, err = c.request(ctx, http.MethodPost, "/api/v1/namespaces", map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]string{"name": namespace},
		})
		if err != nil {
			return fmt.Errorf("create writer Secret namespace: %w", err)
		}
	}

	secretPath := namespacePath + "/secrets/" + url.PathEscape(name)
	status, payload, err := c.request(ctx, http.MethodGet, secretPath, nil)
	if err != nil && status != http.StatusNotFound {
		return err
	}
	authorization := "Bearer " + writerToken
	encodedAuthorization := base64.StdEncoding.EncodeToString([]byte(authorization))
	secret := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"labels": map[string]string{
				generatedByLabel: managedBy,
			},
		},
		"type": "Opaque",
		"data": map[string]string{
			"authorization": encodedAuthorization,
		},
	}
	if status == http.StatusNotFound {
		_, _, err = c.request(ctx, http.MethodPost, namespacePath+"/secrets", secret)
		return redactCredentialError(err, writerToken, authorization, encodedAuthorization)
	}
	var existing struct {
		Metadata struct {
			ResourceVersion string            `json:"resourceVersion"`
			Labels          map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &existing); err != nil {
		return err
	}
	if existing.Metadata.Labels[generatedByLabel] != managedBy {
		return fmt.Errorf("refusing to overwrite unmanaged Secret %s/%s", namespace, name)
	}
	secret["metadata"].(map[string]interface{})["resourceVersion"] = existing.Metadata.ResourceVersion
	_, _, err = c.request(ctx, http.MethodPut, secretPath, secret)
	return redactCredentialError(err, writerToken, authorization, encodedAuthorization)
}

func redactCredentialError(err error, values ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range values {
		if value != "" {
			message = strings.ReplaceAll(message, value, "<redacted>")
		}
	}
	return fmt.Errorf("%s", message)
}

func (c *tenantClient) deleteWriterSecret(ctx context.Context, namespace, name string) error {
	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/secrets/" + url.PathEscape(name)
	status, payload, err := c.request(ctx, http.MethodGet, path, nil)
	if status == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	var existing struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &existing); err != nil {
		return err
	}
	if existing.Metadata.Labels[generatedByLabel] != managedBy {
		return fmt.Errorf("refusing to delete unmanaged Secret %s/%s", namespace, name)
	}
	status, _, err = c.request(ctx, http.MethodDelete, path, nil)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

func withTenantInstaller(ctx context.Context, client *KubeClient, cfg *Config, projectNamespace, instance string, action func(*tenantClient) error) error {
	installer, err := createInstallerAccessKey(ctx, client, cfg, projectNamespace, instance)
	if err != nil {
		return fmt.Errorf("create tenant Secret installer key: %w", err)
	}
	defer func() {
		if err := client.DeleteAccessKey(context.Background(), installer.Metadata.Name); err != nil {
			logError("delete temporary installer AccessKey %s: %v", installer.Metadata.Name, err)
		}
	}()
	tenant, err := newTenantClient(cfg, projectNamespace, instance, installer.Spec.Key)
	if err != nil {
		return err
	}
	return action(tenant)
}

func reconcileWriterCredential(ctx context.Context, client *KubeClient, cfg *Config, projectNamespace, instance string) error {
	writer, err := ensureWriterAccessKey(ctx, client, projectNamespace, instance)
	if err != nil {
		return err
	}
	stateKey := applicationKey(projectNameFromNamespace(projectNamespace), instance)
	if lastSync, ok := writerSecretLastSync[stateKey]; ok && time.Since(lastSync) < cfg.WriterCredentials.SyncInterval {
		return nil
	}
	err = withTenantInstaller(ctx, client, cfg, projectNamespace, instance, func(tenant *tenantClient) error {
		return tenant.ensureWriterSecret(
			ctx,
			cfg.WriterCredentials.SecretNamespace,
			cfg.WriterCredentials.SecretName,
			writer.Spec.Key,
		)
	})
	if err == nil {
		writerSecretLastSync[stateKey] = time.Now()
	}
	return err
}

func deleteWriterCredential(ctx context.Context, client *KubeClient, cfg *Config, projectNamespace, instance string, deleteSecret bool) error {
	project := projectNameFromNamespace(projectNamespace)
	name := credentialName("fleet-observability-writer", project, instance)
	key, err := client.GetAccessKey(ctx, name)
	if err != nil || key == nil {
		return err
	}
	if key.Metadata.Labels[generatedByLabel] != managedBy || key.Metadata.Labels[credentialPurposeLabel] != writerPurpose {
		return fmt.Errorf("refusing to delete unmanaged AccessKey %q", name)
	}
	if deleteSecret {
		if err := withTenantInstaller(ctx, client, cfg, projectNamespace, instance, func(tenant *tenantClient) error {
			return tenant.deleteWriterSecret(ctx, cfg.WriterCredentials.SecretNamespace, cfg.WriterCredentials.SecretName)
		}); err != nil {
			return err
		}
	}
	if err := client.DeleteAccessKey(ctx, name); err != nil {
		return err
	}
	delete(writerSecretLastSync, applicationKey(project, instance))
	logInfo("deleted per-VCI metrics writer AccessKey %s", name)
	return nil
}
