#!/usr/bin/env python3
import json
import logging
import os
import re
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request


API_GROUP = "management.loft.sh"
API_VERSION = "v1"
CLUSTERS_RESOURCE = "clusters"
ARGOCD_APPLICATIONS_RESOURCE = "argocdapplications"
MANAGED_BY = "fleet-binding-controller"


def parse_csv(value):
    if not value:
        return []
    return [item.strip() for item in value.split(",") if item.strip()]


def parse_duration(value):
    value = str(value or "").strip()
    if not value:
        return 30.0
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]+)?)([smh]?)", value)
    if not match:
        raise ValueError(f"invalid duration {value!r}")
    amount = float(match.group(1))
    unit = match.group(2)
    if unit == "h":
        return amount * 3600
    if unit == "m":
        return amount * 60
    return amount


def load_json(value=None, path=None, default=None):
    if path:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    if value:
        return json.loads(value)
    return default


def dns_label(value):
    value = value.lower()
    value = re.sub(r"[^a-z0-9.-]+", "-", value)
    value = value.strip("-.")
    return value or "binding"


def binding_name(app_name, cluster_name):
    name = dns_label(f"{app_name}-{cluster_name}")
    return name[:63].rstrip("-")


def label_value(value):
    value = dns_label(value)
    return value[:63].rstrip("-")


def matches_selector(cluster, selector):
    labels = cluster.get("metadata", {}).get("labels", {}) or {}
    for key, expected in (selector.get("matchLabels") or {}).items():
        if labels.get(key) != str(expected):
            return False
    return True


def ordered_unique(items):
    seen = set()
    result = []
    for item in items:
        if item not in seen:
            seen.add(item)
            result.append(item)
    return result


def desired_bindings_for_cluster(cluster, config):
    metadata = cluster.get("metadata", {})
    annotations = metadata.get("annotations", {}) or {}
    cluster_name = metadata.get("name")
    if not cluster_name or not matches_selector(cluster, config["cluster_selector"]):
        return []

    profiles = parse_csv(annotations.get(config["profile_annotation"]))
    if not profiles:
        profiles = config["default_profiles"]

    apps = []
    for profile in profiles:
        profile_config = config["profiles"].get(profile)
        if profile_config is None:
            logging.warning("cluster %s references unknown profile %s", cluster_name, profile)
            continue
        apps.extend(profile_config.get("apps") or [])

    apps.extend(parse_csv(annotations.get(config["extra_apps_annotation"])))
    skipped = set(parse_csv(annotations.get(config["skip_apps_annotation"])))
    apps = [app for app in ordered_unique(apps) if app not in skipped]

    return [
        {
            "apiVersion": f"{API_GROUP}/{API_VERSION}",
            "kind": "ArgoCDApplication",
            "metadata": {
                "name": binding_name(app, cluster_name),
                "namespace": config["project_namespace"],
                "labels": {
                    "app.kubernetes.io/managed-by": MANAGED_BY,
                    "fleet.lab.kurtmadel.com/generated-by": MANAGED_BY,
                    "fleet.lab.kurtmadel.com/cluster": label_value(cluster_name),
                    "fleet.lab.kurtmadel.com/template": label_value(app),
                },
                "annotations": {
                    "fleet.lab.kurtmadel.com/profiles": ",".join(profiles),
                },
            },
            "spec": {
                "destination": {
                    "cluster": {
                        "name": cluster_name,
                    },
                },
                "templateRef": {
                    "name": app,
                },
            },
        }
        for app in apps
    ]


class KubernetesClient:
    def __init__(self):
        host = os.environ["KUBERNETES_SERVICE_HOST"]
        port = os.environ.get("KUBERNETES_SERVICE_PORT", "443")
        self.base_url = f"https://{host}:{port}"
        with open("/var/run/secrets/kubernetes.io/serviceaccount/token", "r", encoding="utf-8") as handle:
            self.token = handle.read().strip()
        self.context = ssl.create_default_context(
            cafile="/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
        )

    def request(self, method, path, body=None, content_type="application/json"):
        url = self.base_url + path
        headers = {"Authorization": f"Bearer {self.token}"}
        data = None
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = content_type
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, context=self.context, timeout=30) as response:
                payload = response.read()
                if not payload:
                    return None
                return json.loads(payload.decode("utf-8"))
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return None
            message = error.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{method} {path} failed with HTTP {error.code}: {message}") from error

    def list_clusters(self):
        path = f"/apis/{API_GROUP}/{API_VERSION}/{CLUSTERS_RESOURCE}"
        return (self.request("GET", path) or {}).get("items") or []

    def list_argocd_applications(self, namespace):
        path = (
            f"/apis/{API_GROUP}/{API_VERSION}/namespaces/"
            f"{urllib.parse.quote(namespace)}/{ARGOCD_APPLICATIONS_RESOURCE}"
        )
        return (self.request("GET", path) or {}).get("items") or []

    def create_argocd_application(self, namespace, application):
        path = (
            f"/apis/{API_GROUP}/{API_VERSION}/namespaces/"
            f"{urllib.parse.quote(namespace)}/{ARGOCD_APPLICATIONS_RESOURCE}"
        )
        return self.request("POST", path, application)

    def patch_argocd_application(self, namespace, name, application):
        path = (
            f"/apis/{API_GROUP}/{API_VERSION}/namespaces/"
            f"{urllib.parse.quote(namespace)}/{ARGOCD_APPLICATIONS_RESOURCE}/"
            f"{urllib.parse.quote(name)}"
        )
        return self.request("PATCH", path, application, "application/merge-patch+json")

    def delete_argocd_application(self, namespace, name):
        path = (
            f"/apis/{API_GROUP}/{API_VERSION}/namespaces/"
            f"{urllib.parse.quote(namespace)}/{ARGOCD_APPLICATIONS_RESOURCE}/"
            f"{urllib.parse.quote(name)}"
        )
        return self.request("DELETE", path)


def build_config():
    return {
        "project_namespace": os.environ.get("PROJECT_NAMESPACE", "p-platform"),
        "profiles": load_json(
            os.environ.get("FLEET_BINDING_PROFILES_JSON"),
            os.environ.get("FLEET_BINDING_PROFILES_PATH"),
            {},
        ),
        "cluster_selector": load_json(
            os.environ.get("FLEET_BINDING_CLUSTER_SELECTOR_JSON"),
            os.environ.get("FLEET_BINDING_CLUSTER_SELECTOR_PATH"),
            {"matchLabels": {"fleet.lab.kurtmadel.com/baseline": "true"}},
        ),
        "default_profiles": parse_csv(os.environ.get("FLEET_BINDING_DEFAULT_PROFILES"))
        or ["control-plane-baseline"],
        "profile_annotation": os.environ.get(
            "FLEET_BINDING_PROFILE_ANNOTATION",
            "fleet.lab.kurtmadel.com/profiles",
        ),
        "extra_apps_annotation": os.environ.get(
            "FLEET_BINDING_EXTRA_APPS_ANNOTATION",
            "fleet.lab.kurtmadel.com/extra-apps",
        ),
        "skip_apps_annotation": os.environ.get(
            "FLEET_BINDING_SKIP_APPS_ANNOTATION",
            "fleet.lab.kurtmadel.com/skip-apps",
        ),
    }


def reconcile_once(client, config):
    namespace = config["project_namespace"]
    clusters = client.list_clusters()
    desired = {}
    for cluster in clusters:
        for application in desired_bindings_for_cluster(cluster, config):
            desired[application["metadata"]["name"]] = application

    existing_items = client.list_argocd_applications(namespace)
    existing = {
        item.get("metadata", {}).get("name"): item
        for item in existing_items
        if item.get("metadata", {}).get("name")
    }

    for name, application in desired.items():
        if name in existing:
            client.patch_argocd_application(namespace, name, application)
            logging.info("patched ArgoCDApplication %s/%s", namespace, name)
        else:
            client.create_argocd_application(namespace, application)
            logging.info("created ArgoCDApplication %s/%s", namespace, name)

    for name, application in existing.items():
        labels = application.get("metadata", {}).get("labels", {}) or {}
        if labels.get("fleet.lab.kurtmadel.com/generated-by") != MANAGED_BY:
            continue
        if name in desired:
            continue
        client.delete_argocd_application(namespace, name)
        logging.info("deleted stale ArgoCDApplication %s/%s", namespace, name)

    logging.info(
        "reconciled %d desired bindings from %d clusters in namespace %s",
        len(desired),
        len(clusters),
        namespace,
    )


def main():
    logging.basicConfig(
        level=os.environ.get("LOG_LEVEL", "INFO"),
        format="%(asctime)s %(levelname)s %(message)s",
    )
    interval = parse_duration(os.environ.get("RECONCILE_INTERVAL", "30s"))
    config = build_config()
    client = KubernetesClient()
    logging.info(
        "starting %s for namespace %s with profiles: %s",
        MANAGED_BY,
        config["project_namespace"],
        ", ".join(sorted(config["profiles"])),
    )
    while True:
        try:
            reconcile_once(client, config)
        except Exception:
            logging.exception("reconcile failed")
        time.sleep(interval)


if __name__ == "__main__":
    main()
