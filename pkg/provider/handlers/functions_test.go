package handlers

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func Test_BuildLabelsAndAnnotationsFromServiceSpec_Annotations(t *testing.T) {
	container := map[string]string{
		"qwer": "ty",
		"dvor": "ak",
		fmt.Sprintf("%scurrent-time", annotationLabelPrefix): "5 Nov 20:10:20 PST 1955",
		fmt.Sprintf("%sfuture-time", annotationLabelPrefix):  "21 Oct 20:10:20 PST 2015",
	}

	labels, annotation := buildLabelsAndAnnotations(container)

	if len(labels) != 2 {
		t.Errorf("want: %d labels got: %d", 2, len(labels))
	}

	if len(annotation) != 2 {
		t.Errorf("want: %d annotation got: %d", 1, len(annotation))
	}

	if _, ok := annotation["current-time"]; !ok {
		t.Errorf("want: '%s' entry in annotation map got: key not found", "current-time")
	}
}

func Test_SplitMountToSecrets(t *testing.T) {
	type test struct {
		Name     string
		Input    []specs.Mount
		Expected []string
	}
	tests := []test{
		{Name: "No matching openfaas secrets", Input: []specs.Mount{{Destination: "/foo/"}}, Expected: []string{}},
		{Name: "Nil mounts", Input: nil, Expected: []string{}},
		{Name: "No Mounts", Input: []specs.Mount{{Destination: "/foo/"}}, Expected: []string{}},
		{Name: "One Mounts IS secret", Input: []specs.Mount{{Destination: "/var/openfaas/secrets/secret1"}}, Expected: []string{"secret1"}},
		{Name: "Multiple Mounts 1 secret", Input: []specs.Mount{{Destination: "/var/openfaas/secrets/secret1"}, {Destination: "/some/other/path"}}, Expected: []string{"secret1"}},
		{Name: "Multiple Mounts all secrets", Input: []specs.Mount{{Destination: "/var/openfaas/secrets/secret1"}, {Destination: "/var/openfaas/secrets/secret2"}}, Expected: []string{"secret1", "secret2"}},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got := readSecretsFromMounts(tc.Input)
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("expected %s, got %s", tc.Expected, got)
			}
		})
	}
}

func Test_ProcessEnvToEnvVars(t *testing.T) {
	type test struct {
		Name     string
		Input    []string
		Expected map[string]string
		fprocess string
	}
	tests := []test{
		{Name: "No matching EnvVars", Input: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "fprocess=python index.py"}, Expected: make(map[string]string), fprocess: "python index.py"},
		{Name: "No EnvVars", Input: []string{}, Expected: make(map[string]string), fprocess: ""},
		{Name: "One EnvVar", Input: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "fprocess=python index.py", "env=this"}, Expected: map[string]string{"env": "this"}, fprocess: "python index.py"},
		{Name: "Multiple EnvVars", Input: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "this=that", "env=var", "fprocess=python index.py"}, Expected: map[string]string{"this": "that", "env": "var"}, fprocess: "python index.py"},
		{Name: "Nil EnvVars", Input: nil, Expected: make(map[string]string)},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got, fprocess := readEnvFromProcessEnv(tc.Input)
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("expected: %s, got: %s", tc.Expected, got)
			}

			if fprocess != tc.fprocess {
				t.Fatalf("expected fprocess: %s, got: %s", tc.fprocess, got)

			}
		})
	}
}

func Test_findNamespace(t *testing.T) {
	type test struct {
		Name            string
		foundNamespaces []string
		namespace       string
		Expected        bool
	}
	tests := []test{
		{Name: "Namespace Found", namespace: "fn", foundNamespaces: []string{"fn", "openfaas-fn"}, Expected: true},
		{Name: "namespace Not Found", namespace: "fn", foundNamespaces: []string{"openfaas-fn"}, Expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got := findNamespace(tc.namespace, tc.foundNamespaces)
			if got != tc.Expected {
				t.Fatalf("expected %t, got %t", tc.Expected, got)
			}
		})
	}
}
