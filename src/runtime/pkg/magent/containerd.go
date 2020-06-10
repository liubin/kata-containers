package magent

import (
	"context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"

	vc "github.com/kata-containers/kata-containers/src/runtime/virtcontainers"
	"github.com/kata-containers/kata-containers/src/runtime/virtcontainers/pkg/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func getContainer(containersClient containers.Store, namespace, cid string) (containers.Container, error) {
	ctx := context.Background()
	ctx = namespaces.WithNamespace(ctx, namespace)
	return containersClient.Get(ctx, cid)
}

// isSandboxContainer return true if the contaienr is a snadbox container.
func isSandboxContainer(c *containers.Container) bool {
	// unmarshal from any to spec.
	v, err := typeurl.UnmarshalAny(c.Spec)
	if err != nil {
		logrus.WithError(err).Errorf("failed to Unmarshal container spec")
		return false
	}

	// convert to oci spec type
	ociSpec := v.(*specs.Spec)

	// get container type
	containerType, err := oci.ContainerType(*ociSpec)
	if err != nil {
		logrus.WithError(err).Errorf("failed to get contaienr type")
		return false
	}

	// return if is a sandbox container
	return containerType == vc.PodSandbox
}

// getSandboxes get kata sandbox from containerd.
// this should not be called too frequently
func (ma *MAgent) getSandboxes() (map[string]string, error) {
	client, err := containerd.New(ma.containerdAddr)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	ctx := context.Background()

	// first all namespaces.
	namespaceList, err := client.NamespaceService().List(ctx)
	if err != nil {
		return nil, err
	}

	// map of type: <key:sandbox_id => value: namespace>
	sandboxMap := make(map[string]string)

	for _, namespace := range namespaceList {

		initSandboxByNamespaceFunc := func(namespace string) error {
			ctx := context.Background()
			namespacedCtx := namespaces.WithNamespace(ctx, namespace)
			// only listup kata contaienrs pods/containers
			containers, err := client.ContainerService().List(namespacedCtx, "runtime.name=="+kataRuntimeName)
			if err != nil {
				return err
			}

			for i := range containers {
				c := containers[i]
				isc := isSandboxContainer(&c)
				logrus.Debugf("container %s is sandbox container?: %t", c.ID, isc)
				if isc {
					sandboxMap[c.ID] = namespace
				}
			}
			return nil
		}

		if err := initSandboxByNamespaceFunc(namespace); err != nil {
			return nil, err
		}
	}

	return sandboxMap, nil
}
