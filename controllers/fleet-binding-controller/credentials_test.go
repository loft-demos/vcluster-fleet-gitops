package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDesiredWriterAccessKeyHasExactVCIScope(t *testing.T) {
	key, err := desiredWriterAccessKey("p-team-a", "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if key.Spec.Type != "Other" || key.Spec.Subject != "metrics-writer" || key.Spec.Key == "" {
		t.Fatalf("unexpected writer key: %#v", key.Spec)
	}
	if !accessKeyMatchesVCI(&key, "team-a", "tenant-a") {
		t.Fatalf("writer key does not have exact VCI scope: %#v", key.Spec.Scope)
	}
	if len(key.Spec.Scope.Roles) != 1 || key.Spec.Scope.Roles[0].Role != "metrics-writer" {
		t.Fatalf("unexpected roles: %#v", key.Spec.Scope.Roles)
	}
}

func TestTenantClientCreatesAndUpdatesWriterSecret(t *testing.T) {
	var secretExists bool
	var authorization string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer installer-token" {
			t.Errorf("unexpected authorization header %q", request.Header.Get("Authorization"))
		}
		prefix := "/kubernetes/project/default/virtualcluster/tenant-a"
		path := strings.TrimPrefix(request.URL.Path, prefix)
		switch {
		case request.Method == http.MethodGet && path == "/api/v1/namespaces/observability":
			return httpTestResponse(http.StatusOK, ""), nil
		case request.Method == http.MethodGet && path == "/api/v1/namespaces/observability/secrets/otel-otlp-auth":
			if !secretExists {
				return httpTestResponse(http.StatusNotFound, "not found"), nil
			}
			return httpTestResponse(http.StatusOK, `{"metadata":{"resourceVersion":"2","labels":{"fleet.lab.kurtmadel.com/generated-by":"fleet-binding-controller"}}}`), nil
		case (request.Method == http.MethodPost && path == "/api/v1/namespaces/observability/secrets") ||
			(request.Method == http.MethodPut && path == "/api/v1/namespaces/observability/secrets/otel-otlp-auth"):
			var secret struct {
				Data map[string]string `json:"data"`
			}
			if err := json.NewDecoder(request.Body).Decode(&secret); err != nil {
				t.Fatal(err)
			}
			decoded, err := base64.StdEncoding.DecodeString(secret.Data["authorization"])
			if err != nil {
				t.Fatal(err)
			}
			authorization = string(decoded)
			secretExists = true
			return httpTestResponse(http.StatusOK, ""), nil
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			return httpTestResponse(http.StatusBadRequest, "unexpected"), nil
		}
	})

	cfg := &Config{ProjectNamespace: "p-default", WriterCredentials: WriterCredentialsConfig{PlatformURL: "https://platform.example"}}
	client, err := newTenantClient(cfg, "p-default", "tenant-a", "installer-token")
	if err != nil {
		t.Fatal(err)
	}
	client.client = &http.Client{Transport: transport}
	if err := client.ensureWriterSecret(context.Background(), "observability", "otel-otlp-auth", "writer-one"); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer writer-one" {
		t.Fatalf("authorization %q, want Bearer writer-one", authorization)
	}
	if err := client.ensureWriterSecret(context.Background(), "observability", "otel-otlp-auth", "writer-two"); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer writer-two" {
		t.Fatalf("authorization %q, want Bearer writer-two", authorization)
	}
}

func httpTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestCredentialNameAvoidsLongNameCollisions(t *testing.T) {
	a := credentialName("fleet-observability-writer", "project", strings.Repeat("a", 80)+"x")
	b := credentialName("fleet-observability-writer", "project", strings.Repeat("a", 80)+"y")
	if len(a) > 63 || len(b) > 63 || a == b {
		t.Fatalf("credential names are not safe: %q %q", a, b)
	}
}
