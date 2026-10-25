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

// checkNamespaces lists every object in a component's deploy base that a
// namespace override would move. A
// Flux Kustomization's targetNamespace and kustomize's top-level
// namespace field set one namespace on every object, so an object that
// must live elsewhere breaks under either one: an events Role in default
// moved to liken-system made every Event of a cluster-scoped object fail
// with a 403. So each object states no namespace, states liken-system,
// or is cluster-scoped, and a grant that reaches another namespace goes
// through a ClusterRole.
//
// Two exceptions stay, because the ecosystem fixes their namespace:
//
//   - the RoleBinding of an aggregated API to the built-in
//     extension-apiserver-authentication-reader Role, which exists only
//     in kube-system, named <component>-api-authentication-reader in
//     api-authentication.yaml, so a reader finds the one object to
//     exempt from an override;
//   - the dashboards and monitors of a deploy/monitoring component, which
//     a monitoring stack reads from its own namespace.
//
// A base sets liken-system with an unsetOnly NamespaceTransformer, so the
// check also fails on a kustomization.yaml with a top-level namespace.
//
// The manifests the OS applies are out of scope: the OS applies them as
// they are, with no kustomize step that could override a namespace, and
// its Flux feature grants writes to the Secrets of flux-system alone,
// which a ClusterRole would grant in every namespace.
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

// namespaceFiles lists the YAML files of every component's deploy/,
// relative to root and in order.
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

// namespacesIn reads every document of one file and answers a finding for
// each object that a namespace override would move.
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
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if object.Kind == "Kustomization" {
			if object.Namespace != "" {
				findings = append(findings, fmt.Sprintf("%s sets a top-level namespace; set liken-system with an unsetOnly NamespaceTransformer, which leaves an object's own namespace alone", file))
			}
			continue
		}
		namespace := object.Metadata.Namespace
		if namespace == "" || namespace == "liken-system" || exempt(file, object) {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: %s %s is in the namespace %s, and a namespace override would move it; grant across namespaces with a ClusterRole", file, object.Kind, object.Metadata.Name, namespace))
	}
}

// exempt answers whether an object outside liken-system is one of the two
// the rule allows.
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
