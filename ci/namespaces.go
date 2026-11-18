package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// checkNamespaces lists every object in a component's deploy base that
// a namespace override would move to another namespace. A Flux
// Kustomization's targetNamespace and kustomize's top-level namespace
// field both set one namespace on every namespaced object, so an object
// that must be in another namespace breaks when someone deploys the base
// with either one. For example, an operator writes the Event of a
// cluster-scoped object in default. If an override moves the events
// Role from default to liken-system, each of those writes fails with a
// 403.
//
// So each object sets no namespace, sets liken-system, or is
// cluster-scoped, and a grant in another namespace goes through a
// ClusterRole. The check allows these exceptions, whose namespace is
// fixed outside the component:
//
//   - A RoleBinding in kube-system whose name ends in
//     -api-authentication-reader, in a file named
//     api-authentication.yaml. An aggregated API binds the built-in
//     Role extension-apiserver-authentication-reader, which exists
//     only in kube-system.
//   - An object in monitoring under a deploy/monitoring/ directory,
//     because the monitoring stack reads its dashboards and monitors
//     from its own namespace.
//
// A base sets liken-system with an unsetOnly NamespaceTransformer,
// which does not change an object that sets its own namespace. So the
// check also fails on a kustomization.yaml with a top-level namespace.
//
// The check reads only the deploy/ directory of each component, so it
// does not read the OS's other manifests. The OS applies those as they
// are, with no kustomize step. Its Flux manifests grant rights in the
// flux-system namespace only, and a ClusterRole would grant the same
// rights in every namespace.
func checkNamespaces(root string) ([]string, error) {
	files, err := namespaceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return nil, err
		}
		found, err := namespacesIn(file, data)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// namespaceFiles returns the YAML files under each component's
// deploy/, relative to root and sorted.
func namespaceFiles(root string) ([]string, error) {
	dirs, err := filepath.Glob(filepath.Join(root, "*", "deploy"))
	if err != nil {
		return nil, err
	}
	var files []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if ext := path.Ext(p); ext == ".yaml" || ext == ".yml" {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(files)
	return files, nil
}

// namespacedObject is the part of a manifest the check reads.
type namespacedObject struct {
	Kind      string `yaml:"kind"`
	Namespace string `yaml:"namespace"`
	Metadata  struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
}

func namespacesIn(file string, data []byte) ([]string, error) {
	var findings []string
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var object namespacedObject
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return findings, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", file, err)
		}
		if object.Kind == "Kustomization" {
			if object.Namespace != "" {
				findings = append(findings, fmt.Sprintf("%s sets a top-level namespace. Set liken-system with an unsetOnly NamespaceTransformer, which leaves an object's own namespace alone", file))
			}
			continue
		}
		namespace := object.Metadata.Namespace
		if namespace == "" || namespace == "liken-system" || exempt(file, object) {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: %s %s is in the namespace %s, and a namespace override would move it. Grant across namespaces with a ClusterRole", file, object.Kind, object.Metadata.Name, namespace))
	}
}

// exempt reports whether an object outside liken-system is one of the
// exceptions that checkNamespaces allows.
func exempt(file string, object namespacedObject) bool {
	switch object.Metadata.Namespace {
	case "kube-system":
		return path.Base(file) == "api-authentication.yaml" &&
			object.Kind == "RoleBinding" &&
			strings.HasSuffix(object.Metadata.Name, "-api-authentication-reader")
	case "monitoring":
		return strings.Contains(file, "/deploy/monitoring/")
	}
	return false
}
