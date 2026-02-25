package kubevirt

import (
	"fmt"
	"reflect"

	kubevirtclient "kubevirt.io/client-go/kubevirt/typed/core/v1"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	"github.com/projectcalico/calico/libcalico-go/lib/apis/internalapi"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/api"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/k8s"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/k8s/resources"
	"github.com/projectcalico/calico/libcalico-go/lib/backend/model"
)

func Enable(client api.Client, ca *apiconfig.CalicoAPIConfigSpec) error {
	c, ok := client.(*k8s.KubeClient)
	if !ok {
		return fmt.Errorf("%v is not a KubeClient", client)
	}
	config, _, err := k8s.CreateKubernetesClientset(ca)
	if err != nil {
		return fmt.Errorf("in kubevirt.Enable(): %w", err)
	}
	kvClient, err := kubevirtclient.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to build KubeVirt client: %v", err)
	}
	c.RegisterResourceClient(
		reflect.TypeOf(model.ResourceKey{}),
		reflect.TypeOf(model.ResourceListOptions{}),
		internalapi.KindLiveMigration,
		resources.NewLiveMigrationClient(func(namespace string) resources.VMIMClient {
			return kvClient.VirtualMachineInstanceMigrations(namespace)
		}),
	)
	return nil
}
