// Copyright (c) 2017-2025 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resources

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	apiv3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	log "github.com/sirupsen/logrus"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	scheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
	"github.com/projectcalico/calico/libcalico-go/lib/names"
)

// customK8sResourceClient implements the K8sResourceClient interface and provides a generic
// mechanism for a 1:1 mapping between a Calico Resource and an equivalent Kubernetes
// custom resource type.
type customK8sResourceClient struct {
	clientSet  kubernetes.Interface
	restClient rest.Interface

	// Name of the CRD. Not used.
	name string

	// resource is the kind of the CRD managed by this client, used as part of the
	// endpoint generated for Kubernetes API calls.
	resource string

	// CRD description. Not used.
	description string

	// Types used to generate the returned structs.
	k8sResourceType reflect.Type
	k8sListType     reflect.Type

	// k8sResourceTypeMeta is the TypeMeta to set for all resources
	// returned by this client. It is used to set the GroupVersion.
	k8sResourceTypeMeta metav1.TypeMeta

	// Whether or not the CRD managed by this is namespaced. Used for generating
	// Kubernetes API call endpoints.
	namespaced bool

	// resourceKind is the kind to set for TypeMeta.Kind for all resources
	// returned by this client.
	resourceKind string

	// versionConverter is an optional hook to convert the returned data from the CRD
	// from one format to another before returning it to the caller.
	versionconverter VersionConverter

	// validator used to validate resources.
	validator Validator
}

// VersionConverter converts v1 or v3 k8s resources into v3 resources.
// For a v3 resource, the conversion should be a no-op.
type VersionConverter interface {
	ConvertFromK8s(Resource) (Resource, error)
}

// Validator validates a resource.
type Validator interface {
	Validate(Resource) error
}

// Create creates a new Custom K8s Resource instance in the k8s API from the supplied KVPair.
func (c *customK8sResourceClient) Create(ctx context.Context, kvp *model.KVPair) (*model.KVPair, error) {
	logContext := log.WithFields(log.Fields{
		"Key":      kvp.Key,
		"Value":    kvp.Value,
		"Resource": c.resource,
	})
	logContext.Debug("Create custom Kubernetes resource")

	// Convert the KVPair to the K8s resource.
	resIn, err := c.convertKVPairToResource(kvp)
	if err != nil {
		logContext.WithError(err).Debug("Error converting to k8s resource")
		return nil, err
	}

	// Validate the resource if the Validator is defined.
	if c.validator != nil {
		if err = c.validator.Validate(resIn); err != nil {
			logContext.WithError(err).Debug("Error creating resource")
			return nil, err
		}
	}

	// Send the update request using the REST interface.
	resOut := reflect.New(c.k8sResourceType).Interface().(Resource)
	namespace := kvp.Key.(model.ResourceKey).Namespace
	err = c.restClient.Post().
		NamespaceIfScoped(namespace, c.namespaced).
		Resource(c.resource).
		Body(resIn).
		Do(ctx).Into(resOut)
	if err != nil {
		logContext.WithError(err).Debug("Error creating resource")
		return nil, K8sErrorToCalico(err, kvp.Key)
	}

	// Update the return data with the metadata populated by the (Kubernetes) datastore.
	kvp, err = c.convertResourceToKVPair(resOut)
	if err != nil {
		logContext.WithError(err).Debug("Error converting created K8s resource to Calico resource")
		return nil, K8sErrorToCalico(err, kvp.Key)
	}
	// Update the revision information from the response.
	kvp.Revision = resOut.GetObjectMeta().GetResourceVersion()

	return kvp, nil
}

// Update updates an existing Custom K8s Resource instance in the k8s API from the supplied KVPair.
func (c *customK8sResourceClient) Update(ctx context.Context, kvp *model.KVPair) (*model.KVPair, error) {
	logContext := log.WithFields(log.Fields{
		"Key":      kvp.Key,
		"Value":    kvp.Value,
		"Resource": c.resource,
	})

	// Create storage for the updated resource.
	resOut := reflect.New(c.k8sResourceType).Interface().(Resource)

	var updateError error
	// Convert the KVPair to a K8s resource.
	resIn, err := c.convertKVPairToResource(kvp)
	if err != nil {
		logContext.WithError(err).Debug("Error updating resource")
		return nil, err
	}

	// Validate the resource if the Validator is defined.
	if c.validator != nil {
		if err = c.validator.Validate(resIn); err != nil {
			logContext.WithError(err).Debug("Error updating resource")
			return nil, err
		}
	}

	// Send the update request using the name.
	name := resIn.GetObjectMeta().GetName()
	name = c.defaultPolicyName(name)
	namespace := resIn.GetObjectMeta().GetNamespace()
	logContext = logContext.WithField("Name", name)
	logContext.Debug("Update resource by name")
	updateError = c.restClient.Put().
		Resource(c.resource).
		NamespaceIfScoped(namespace, c.namespaced).
		Body(resIn).
		Name(name).
		Do(ctx).Into(resOut)
	if updateError != nil {
		// Failed to update the resource.
		logContext.WithError(updateError).Error("Error updating resource")
		return nil, K8sErrorToCalico(updateError, kvp.Key)
	}

	// Update the return data with the metadata populated by the (Kubernetes) datastore.
	kvp, err = c.convertResourceToKVPair(resOut)
	if err != nil {
		logContext.WithError(err).Debug("Error converting created K8s resource to Calico resource")
		return nil, K8sErrorToCalico(err, kvp.Key)
	}
	// Success. Update the revision information from the response.
	kvp.Revision = resOut.GetObjectMeta().GetResourceVersion()

	return kvp, nil
}

// UpdateStatus updates status section of an existing Custom K8s Resource instance in the k8s API from the supplied KVPair.
func (c *customK8sResourceClient) UpdateStatus(ctx context.Context, kvp *model.KVPair) (*model.KVPair, error) {
	logContext := log.WithFields(log.Fields{
		"Key":             kvp.Key,
		"Value":           kvp.Value,
		"Parent Resource": c.resource,
	})
	logContext.Debug("UpdateStatus custom Kubernetes resource")

	// Create storage for the updated resource.
	resOut := reflect.New(c.k8sResourceType).Interface().(Resource)

	var updateError error
	// Convert the KVPair to a K8s resource.
	resIn, err := c.convertKVPairToResource(kvp)
	if err != nil {
		logContext.WithError(err).Debug("Error updating resource status")
		return nil, err
	}

	// Send the update request using the name.
	name := resIn.GetObjectMeta().GetName()
	name = c.defaultPolicyName(name)
	namespace := resIn.GetObjectMeta().GetNamespace()
	logContext = logContext.WithField("Name", name)
	logContext.Debug("Update resource status by name")
	updateError = c.restClient.Put().
		Resource(c.resource).
		SubResource("status").
		NamespaceIfScoped(namespace, c.namespaced).
		Body(resIn).
		Name(name).
		Do(ctx).Into(resOut)
	if updateError != nil {
		// Failed to update the resource.
		logContext.WithError(updateError).Error("Error updating resource status")
		return nil, K8sErrorToCalico(updateError, kvp.Key)
	}

	// Update the return data with the metadata populated by the (Kubernetes) datastore.
	kvp, err = c.convertResourceToKVPair(resOut)
	if err != nil {
		logContext.WithError(err).Debug("Error converting returned K8s resource to Calico resource")
		return nil, K8sErrorToCalico(err, kvp.Key)
	}
	// Success. Update the revision information from the response.
	kvp.Revision = resOut.GetObjectMeta().GetResourceVersion()

	return kvp, nil
}

func (c *customK8sResourceClient) DeleteKVP(ctx context.Context, kvp *model.KVPair) (*model.KVPair, error) {
	return c.Delete(ctx, kvp.Key, kvp.Revision, kvp.UID)
}

// Delete deletes an existing Custom K8s Resource instance in the k8s API using the supplied KVPair.
func (c *customK8sResourceClient) Delete(ctx context.Context, k model.Key, revision string, uid *types.UID) (*model.KVPair, error) {
	logContext := log.WithFields(log.Fields{
		"Key":      k,
		"Resource": c.resource,
	})
	logContext.Debug("Delete custom Kubernetes resource")

	// Convert the Key to a resource name.
	name, err := c.keyToName(k)
	if err != nil {
		logContext.WithError(err).Debug("Error deleting resource")
		return nil, err
	}
	name = c.defaultPolicyName(name)

	existing, err := c.Get(ctx, k, revision)
	if err != nil {
		return nil, err
	}

	namespace := k.(model.ResourceKey).Namespace

	opts := &metav1.DeleteOptions{}
	if uid != nil {
		// The UID in the v3 resources is a translation of the UID in the CR. Translate it
		// before passing as a precondition.
		uid, err := conversion.ConvertUID(*uid)
		if err != nil {
			return nil, err
		}
		opts.Preconditions = &metav1.Preconditions{UID: &uid}
	}

	// Delete the resource using the name.
	logContext = logContext.WithField("Name", name)
	logContext.Debug("Send delete request by name")
	err = c.restClient.Delete().
		NamespaceIfScoped(namespace, c.namespaced).
		Resource(c.resource).
		Name(name).
		Body(opts).
		Do(ctx).
		Error()
	if err != nil {
		logContext.WithError(err).Debug("Error deleting resource")
		return nil, K8sErrorToCalico(err, k)
	}
	return existing, nil
}

// Get gets an existing Custom K8s Resource instance in the k8s API using the supplied Key.
func (c *customK8sResourceClient) Get(ctx context.Context, key model.Key, revision string) (*model.KVPair, error) {
	logContext := log.WithFields(log.Fields{
		"Key":      key,
		"Resource": c.resource,
		"Revision": revision,
	})
	logContext.Debug("Get custom Kubernetes resource")
	name, err := c.keyToName(key)
	if err != nil {
		logContext.WithError(err).Debug("Error getting resource")
		return nil, err
	}
	name = c.defaultPolicyName(name)
	namespace := key.(model.ResourceKey).Namespace

	// Add the name and namespace to the log context now that we know it, and query Kubernetes.
	logContext = logContext.WithFields(log.Fields{"Name": name, "Namespace": namespace})

	logContext.Debug("Get custom Kubernetes resource by name")
	resOut := reflect.New(c.k8sResourceType).Interface().(Resource)
	err = c.restClient.Get().
		NamespaceIfScoped(namespace, c.namespaced).
		Resource(c.resource).
		Name(name).
		Do(ctx).Into(resOut)
	if err != nil {
		logContext.WithError(err).Debug("Error getting resource")
		return nil, K8sErrorToCalico(err, key)
	}

	return c.convertResourceToKVPair(resOut)
}

// List lists configured Custom K8s Resource instances in the k8s API matching the
// supplied ListInterface. It will use list paging if necessary to reduce the load on the Kubernetes API server.
func (c *customK8sResourceClient) List(ctx context.Context, list model.ListInterface, revision string) (*model.KVPairList, error) {
	logContext := log.WithFields(log.Fields{
		"ListInterface": list,
		"Resource":      c.resource,
		"Type":          "CustomResource",
	})
	logContext.Debug("Received List request")

	// If it is a namespaced resource, then we'll need the namespace.
	resList := list.(model.ResourceListOptions)
	namespace := resList.Namespace
	key := c.listInterfaceToKey(list)

	// listFunc performs a list with the given options.
	listFunc := func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		out := reflect.New(c.k8sListType).Interface().(ResourceList)

		if key != nil {
			// Being asked to list a single resource, add the filter.
			key := key.(model.ResourceKey)
			name, err := c.keyToName(key)
			if err != nil {
				logContext.WithError(err).Error("Failed to convert key to name of resource.")
				return nil, err
			}
			opts.FieldSelector = fmt.Sprintf("metadata.name=%s", name)
		}

		// Build the request.
		req := c.restClient.Get().
			NamespaceIfScoped(namespace, c.namespaced).
			Resource(c.resource).
			VersionedParams(&opts, scheme.ParameterCodec)

		// If the prefix is specified, look for the resources with the label
		// of prefix.
		if resList.Prefix {
			// The prefix has a trailing "." character, remove it, since it is not valid for k8s labels
			if !strings.HasSuffix(resList.Name, ".") {
				return nil, errors.New("internal error: custom resource list invoked for a prefix not in the form '<tier>.'")
			}
			name := resList.Name[:len(resList.Name)-1]
			if name == "default" {
				req = req.VersionedParams(&metav1.ListOptions{
					LabelSelector: "!" + apiv3.LabelTier,
				}, scheme.ParameterCodec)
			} else {
				req = req.VersionedParams(&metav1.ListOptions{
					LabelSelector: apiv3.LabelTier + "=" + name,
				}, scheme.ParameterCodec)
			}
		}

		// Perform the request.
		err := req.Do(ctx).Into(out)
		if err != nil {
			// Don't return errors for "not found".  This just
			// means there are no matching Custom K8s Resources, and we should return
			// an empty list.
			if !kerrors.IsNotFound(err) {
				log.WithError(err).Debug("Error listing resources")
				return nil, err
			}
		}
		return out, nil
	}

	convertFunc := func(r Resource) ([]*model.KVPair, error) {
		kvp, err := c.convertResourceToKVPair(r)
		if err != nil {
			return nil, err
		}
		return []*model.KVPair{kvp}, nil
	}
	return pagedList(ctx, logContext, revision, list, convertFunc, listFunc)
}

func (c *customK8sResourceClient) Watch(ctx context.Context, list model.ListInterface, options api.WatchOptions) (api.WatchInterface, error) {
	rlo, ok := list.(model.ResourceListOptions)
	if !ok {
		return nil, fmt.Errorf("ListInterface is not a ResourceListOptions: %s", list)
	}
	fieldSelector := fields.Everything()
	if len(rlo.Name) != 0 {
		// We've been asked to watch a specific custom resource.
		log.WithField("name", rlo.Name).Debug("Watching a single custom resource")
		fieldSelector = fields.OneTermEqualSelector("metadata.name", rlo.Name)

		// If this is a namespaced resource, we also need the namespace specified.
		if c.namespaced && rlo.Namespace == "" {
			return nil, fmt.Errorf("name present, but missing namespace on watch request")
		}
	}

	k8sWatchClient := cache.NewListWatchFromClient(c.restClient, c.resource, rlo.Namespace, fieldSelector)
	k8sOpts := watchOptionsToK8sListOptions(options)
	k8sWatch, err := k8sWatchClient.WatchFunc(k8sOpts)
	if err != nil {
		return nil, K8sErrorToCalico(err, list)
	}
	toKVPair := func(r Resource) (*model.KVPair, error) {
		return c.convertResourceToKVPair(r)
	}

	return newK8sWatcherConverter(ctx, rlo.Kind+" (custom)", toKVPair, k8sWatch), nil
}

// EnsureInitialized is a no-op since the CRD should be
// initialized in advance.
func (c *customK8sResourceClient) EnsureInitialized() error {
	return nil
}

func (c *customK8sResourceClient) listInterfaceToKey(l model.ListInterface) model.Key {
	pl := l.(model.ResourceListOptions)
	key := model.ResourceKey{Name: pl.Name, Kind: pl.Kind}

	if c.namespaced && pl.Namespace != "" {
		key.Namespace = pl.Namespace
	}

	if pl.Name != "" && !pl.Prefix {
		return key
	}
	return nil
}

func (c *customK8sResourceClient) keyToName(k model.Key) (string, error) {
	return k.(model.ResourceKey).Name, nil
}

func (c *customK8sResourceClient) nameToKey(name string) (model.Key, error) {
	return model.ResourceKey{
		Name: name,
		Kind: c.resourceKind,
	}, nil
}

func (c *customK8sResourceClient) convertResourceToKVPair(r Resource) (*model.KVPair, error) {
	var err error

	// If the resource has a VersionConverter defined then pass the resource through
	// the VersionConverter to convert the resource version from v1 to v3.
	// No-op for a v3 resource.
	if c.versionconverter != nil {
		if r, err = c.versionconverter.ConvertFromK8s(r); err != nil {
			return nil, fmt.Errorf("error converting resource from v1 to v3: %s", err)
		}
	}

	gvk := c.k8sResourceTypeMeta.GetObjectKind().GroupVersionKind()
	gvk.Kind = c.resourceKind
	r.GetObjectKind().SetGroupVersionKind(gvk)
	kvp := &model.KVPair{
		Key: model.ResourceKey{
			Name:      r.GetObjectMeta().GetName(),
			Namespace: r.GetObjectMeta().GetNamespace(),
			Kind:      c.resourceKind,
		},
		Revision: r.GetObjectMeta().GetResourceVersion(),
	}

	if err := ConvertK8sResourceToCalicoResource(r); err != nil {
		return kvp, err
	}

	kvp.Value = r
	return kvp, nil
}

func (c *customK8sResourceClient) convertKVPairToResource(kvp *model.KVPair) (Resource, error) {
	resource := kvp.Value.(Resource)
	resource.GetObjectMeta().SetResourceVersion(kvp.Revision)
	resOut, err := ConvertCalicoResourceToK8sResource(resource)
	if err != nil {
		return resOut, err
	}

	return resOut, nil
}

func (c *customK8sResourceClient) defaultPolicyName(name string) string {
	if c.resourceKind == apiv3.KindGlobalNetworkPolicy ||
		c.resourceKind == apiv3.KindNetworkPolicy ||
		c.resourceKind == apiv3.KindStagedGlobalNetworkPolicy ||
		c.resourceKind == apiv3.KindStagedNetworkPolicy {
		// Policies in default tier are stored in the backend with the default prefix, if the prefix is not present we prefix it now
		name = names.TieredPolicyName(name)
	}

	return name
}
