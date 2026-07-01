import unittest

import controller


CONFIG = {
    "project_namespace": "p-platform",
    "profiles": {
        "control-plane-baseline": {
            "apps": ["cert-manager", "metrics-server", "metallb"],
        },
        "vcp-management-cluster-baseline": {
            "apps": ["cert-manager", "metrics-server", "vcluster-gitops-watcher"],
        },
        "gpu-nvidia-baseline": {
            "apps": ["nvidia-gpu-operator", "nvidia-dra-driver-gpu"],
        },
    },
    "cluster_selector": {
        "matchLabels": {
            "fleet.lab.kurtmadel.com/baseline": "true",
        },
    },
    "default_profiles": ["control-plane-baseline"],
    "profile_annotation": "fleet.lab.kurtmadel.com/profiles",
    "extra_apps_annotation": "fleet.lab.kurtmadel.com/extra-apps",
    "skip_apps_annotation": "fleet.lab.kurtmadel.com/skip-apps",
}


def cluster(name, labels=None, annotations=None):
    return {
        "metadata": {
            "name": name,
            "labels": labels or {},
            "annotations": annotations or {},
        }
    }


class DesiredBindingsTest(unittest.TestCase):
    def test_default_profile_is_used_for_selected_cluster(self):
        bindings = controller.desired_bindings_for_cluster(
            cluster(
                "cp-blacksburg-dc1",
                labels={"fleet.lab.kurtmadel.com/baseline": "true"},
            ),
            CONFIG,
        )

        self.assertEqual(
            [binding["spec"]["templateRef"]["name"] for binding in bindings],
            ["cert-manager", "metrics-server", "metallb"],
        )

    def test_profile_annotation_can_combine_profiles(self):
        bindings = controller.desired_bindings_for_cluster(
            cluster(
                "gpu-cluster",
                labels={"fleet.lab.kurtmadel.com/baseline": "true"},
                annotations={
                    "fleet.lab.kurtmadel.com/profiles": (
                        "control-plane-baseline,gpu-nvidia-baseline"
                    ),
                },
            ),
            CONFIG,
        )

        self.assertEqual(
            [binding["spec"]["templateRef"]["name"] for binding in bindings],
            [
                "cert-manager",
                "metrics-server",
                "metallb",
                "nvidia-gpu-operator",
                "nvidia-dra-driver-gpu",
            ],
        )

    def test_extra_and_skip_annotations_adjust_apps(self):
        bindings = controller.desired_bindings_for_cluster(
            cluster(
                "edge",
                labels={"fleet.lab.kurtmadel.com/baseline": "true"},
                annotations={
                    "fleet.lab.kurtmadel.com/extra-apps": "custom-app",
                    "fleet.lab.kurtmadel.com/skip-apps": "metallb",
                },
            ),
            CONFIG,
        )

        self.assertEqual(
            [binding["spec"]["templateRef"]["name"] for binding in bindings],
            ["cert-manager", "metrics-server", "custom-app"],
        )

    def test_unselected_cluster_gets_no_bindings(self):
        bindings = controller.desired_bindings_for_cluster(cluster("other"), CONFIG)
        self.assertEqual(bindings, [])


if __name__ == "__main__":
    unittest.main()
