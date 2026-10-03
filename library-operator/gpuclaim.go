package main

// gpuclaim.go is the GPU claim of a heavy fact's worker. The cluster owner
// writes a ResourceClaimTemplate in the Library's namespace, and the Library
// names it in spec.trickplay.gpuResourceClaimTemplate or
// spec.appearances.gpuResourceClaimTemplate. The worker's pod names the
// template, and the resource claim controller creates one ResourceClaim from
// it for each pod.
//
// The operator reads no part of the template's spec. A claim can take any
// shape that dynamic resource allocation allows, such as a render node paired
// with a media.liken.sh capability device of the same GPU, and the choice of
// a GPU is the cluster owner's. So the operator only reads whether the
// template exists, because a pod that names a missing template stays Pending
// until its Job's deadline.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// The group and version of the dynamic resource allocation objects.
const deviceAPIVersion = "resource.k8s.io/v1"

// The name of the claim in a worker's pod, which its container repeats under
// resources.claims.
const gpuClaimName = "render"

// A ResourceClaimTemplate, of which the operator reads only the metadata: the
// name, which says whether the template exists, and the labels and owner
// references, which say whether an earlier release of this operator created
// it.
type ResourceClaimTemplate struct {
	APIVersion string     `json:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Metadata   ObjectMeta `json:"metadata"`
}

// The claim a worker's pod holds, and nil where the Library names no template
// for the worker.
func gpuClaim(library *Library, worker factWorker) *PodResourceClaim {
	template := worker.gpuClaimTemplate(library)
	if template == "" {
		return nil
	}
	return &PodResourceClaim{Name: gpuClaimName, ResourceClaimTemplateName: template}
}

// One template that an enabled worker of a Library names, and whether the
// Library's namespace holds it.
type gpuClaimTemplate struct {
	fact  string
	name  string
	found bool
}

// The templates that the enabled workers of one Library name, read once in a
// pass. The worker step reads it to hold back a worker whose template is
// missing, and the status reads it for the GPUClaimTemplates condition.
type gpuClaimTemplates []gpuClaimTemplate

// Whether the worker of a fact names a template that does not exist.
func (t gpuClaimTemplates) missing(fact string) bool {
	return slices.ContainsFunc(t, func(one gpuClaimTemplate) bool { return one.fact == fact && !one.found })
}

// readGPUClaimTemplates reads each template that an enabled worker names. A
// disabled worker starts no Job, so its template is not read. The pass reads
// each template from the watch of every template in the cluster, so a
// template the cluster owner creates wakes the pass that starts the worker.
func (o *operator) readGPUClaimTemplates(ctx context.Context, library *Library) (gpuClaimTemplates, error) {
	var templates gpuClaimTemplates
	for _, worker := range factWorkers {
		name := worker.gpuClaimTemplate(library)
		if !worker.enabled(library) || name == "" {
			continue
		}
		_, err := o.watched.readClaimTemplate(ctx, library.Metadata.Namespace, name)
		if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			return nil, fmt.Errorf("reading the ResourceClaimTemplate %s: %w", name, err)
		}
		templates = append(templates, gpuClaimTemplate{fact: worker.fact, name: name, found: err == nil})
	}
	return templates, nil
}

// The GPUClaimTemplates condition: True when every template the enabled
// workers name exists, and False with a message for each one that does not.
// The second answer is false where no enabled worker names a template, and
// such a Library carries no GPUClaimTemplates condition.
func gpuClaimTemplatesCondition(templates gpuClaimTemplates, namespace string, generation int64) (Condition, bool) {
	if len(templates) == 0 {
		return Condition{}, false
	}
	var missing []string
	for _, one := range templates {
		if !one.found {
			missing = append(missing, fmt.Sprintf(
				"the ResourceClaimTemplate %s that spec.%s.gpuResourceClaimTemplate names does not exist "+
					"in namespace %s, so the %s worker does not start", one.name, one.fact, namespace, one.fact))
		}
	}
	condition := Condition{
		Type:               conditionGPUClaimTemplates,
		Status:             ConditionTrue,
		ObservedGeneration: generation,
		Reason:             reasonClaimTemplatesFound,
		Message:            "every ResourceClaimTemplate the workers name exists",
	}
	if len(missing) > 0 {
		condition.Status = ConditionFalse
		condition.Reason = reasonClaimTemplateNotFound
		condition.Message = strings.Join(missing, "; ")
	}
	return condition, true
}

// The name an earlier release of this operator gave the template it created
// for one worker of a Library.
func formerClaimTemplateName(library, fact string) string {
	return library + "-" + fact
}

// retireOwnedClaimTemplates deletes the templates that an earlier release of
// this operator created for the Library. The Library now names a template
// that the cluster owner writes, so the operator's own templates are claimed
// by no pod. Each one carries an owner reference to the Library's UID, and a
// template a person wrote at the same name does not, so the operator deletes
// only a template that carries that reference.
//
// The pass reads both names from the watch of every template, so a Library
// with no such template costs the API server no request.
func (o *operator) retireOwnedClaimTemplates(ctx context.Context, library *Library) error {
	namespace := library.Metadata.Namespace
	for _, worker := range factWorkers {
		name := formerClaimTemplateName(library.Metadata.Name, worker.fact)
		template, err := o.watched.readClaimTemplate(ctx, namespace, name)
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading the ResourceClaimTemplate %s: %w", name, err)
		}
		if !ownedBy(template.Metadata, library) {
			continue
		}
		if err := o.deleteClaimTemplate(ctx, namespace, name); err != nil {
			return fmt.Errorf("deleting the ResourceClaimTemplate %s: %w", name, err)
		}
		o.logf("library %s/%s: deleted the ResourceClaimTemplate %s, which an earlier release created",
			namespace, library.Metadata.Name, name)
	}
	return nil
}

// Whether an object names the Library as its owner, by the Library's UID.
func ownedBy(meta ObjectMeta, library *Library) bool {
	return slices.ContainsFunc(meta.OwnerReferences, func(owner OwnerReference) bool {
		return owner.Kind == "Library" && owner.UID == library.Metadata.UID
	})
}

// deleteClaimTemplate deletes one template and notes that the operator holds
// no copy of it, so the next read goes to the API server while the watch's
// store still holds the deleted copy.
func (o *operator) deleteClaimTemplate(ctx context.Context, namespace, name string) error {
	return o.versions.claimTemplates.Send(memo.Key(&ObjectMeta{Namespace: namespace, Name: name}), func() (string, error) {
		return "", DeleteResourceClaimTemplate(ctx, o.client, namespace, name)
	})
}

func claimTemplatesPath(namespace string) string {
	return "/apis/" + deviceAPIVersion + "/namespaces/" + namespace + "/resourceclaimtemplates"
}

func GetResourceClaimTemplate(ctx context.Context, c *apiclient.Client, namespace, name string) (*ResourceClaimTemplate, error) {
	held := &ResourceClaimTemplate{}
	if err := c.WithContext(ctx).RequestJSON(http.MethodGet, claimTemplatesPath(namespace)+"/"+name, nil, held); err != nil {
		return nil, err
	}
	return held, nil
}

// An already-absent template is success, the rule every other delete here
// follows.
func DeleteResourceClaimTemplate(ctx context.Context, c *apiclient.Client, namespace, name string) error {
	err := c.WithContext(ctx).RequestJSON(http.MethodDelete, claimTemplatesPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}
