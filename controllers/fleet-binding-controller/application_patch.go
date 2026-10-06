package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

// appliedApplicationPatch remembers the last patch sent for an Application
// and the resourceVersion it left behind.
type appliedApplicationPatch struct {
	hash            string
	resourceVersion string
}

// appliedApplicationPatches is keyed by applicationKey. It is process memory
// only: after a restart each Application is checked against the live object
// again.
var appliedApplicationPatches = map[string]appliedApplicationPatch{}

// applicationNeedsPatch reports whether sending patch would change the
// existing Application. It applies the merge patch to the live object locally
// and compares the result. As a backstop for fields the API server defaults
// or normalizes after a patch, it also skips a patch identical to the last one
// sent when the object has not changed since.
func applicationNeedsPatch(key string, existing Application, patch applicationPatch) bool {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return true
	}
	if last, ok := appliedApplicationPatches[key]; ok &&
		last.hash == hashJSON(patchJSON) &&
		last.resourceVersion != "" &&
		last.resourceVersion == existing.Metadata.ResourceVersion {
		return false
	}

	var current, changes map[string]interface{}
	if err := roundTripJSON(existing, &current); err != nil {
		return true
	}
	if err := json.Unmarshal(patchJSON, &changes); err != nil {
		return true
	}
	before := patchedFields(current)
	after := patchedFields(mergePatchJSON(current, changes).(map[string]interface{}))
	return !reflect.DeepEqual(before, after)
}

func rememberApplicationPatch(key string, patch applicationPatch, resourceVersion string) {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return
	}
	appliedApplicationPatches[key] = appliedApplicationPatch{hash: hashJSON(patchJSON), resourceVersion: resourceVersion}
}

func forgetApplicationPatch(key string) {
	delete(appliedApplicationPatches, key)
}

// patchedFields extracts the parts of an Application the controller patches,
// so unrelated changes such as status do not count as drift.
func patchedFields(object map[string]interface{}) map[string]interface{} {
	metadata, _ := object["metadata"].(map[string]interface{})
	return map[string]interface{}{
		"labels":      emptyAsNil(metadata["labels"]),
		"annotations": emptyAsNil(metadata["annotations"]),
		"spec":        object["spec"],
	}
}

// emptyAsNil treats a missing and an empty map the same, as the API server does.
func emptyAsNil(value interface{}) interface{} {
	if m, ok := value.(map[string]interface{}); ok && len(m) == 0 {
		return nil
	}
	return value
}

// mergePatchJSON applies an RFC 7386 JSON merge patch to target and returns
// the result. Objects merge recursively, null deletes a key, and any other
// value replaces the target value.
func mergePatchJSON(target, patch interface{}) interface{} {
	patchObject, ok := patch.(map[string]interface{})
	if !ok {
		return patch
	}
	targetObject, ok := target.(map[string]interface{})
	if !ok {
		targetObject = map[string]interface{}{}
	}
	result := make(map[string]interface{}, len(targetObject))
	for k, v := range targetObject {
		result[k] = v
	}
	for k, v := range patchObject {
		if v == nil {
			delete(result, k)
			continue
		}
		result[k] = mergePatchJSON(result[k], v)
	}
	return result
}

func roundTripJSON(in interface{}, out interface{}) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func hashJSON(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
