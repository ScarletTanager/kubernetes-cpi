package actions

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ScarletTanager/kubernetes-cpi/agent"
	"github.com/ScarletTanager/kubernetes-cpi/config"
	"github.com/ScarletTanager/kubernetes-cpi/cpi"
	"github.com/ScarletTanager/kubernetes-cpi/kubecluster"

	core "k8s.io/client-go/kubernetes/typed/core/v1"
	kubeerrors "k8s.io/client-go/pkg/api/errors"
	"k8s.io/client-go/pkg/api/resource"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
)

type VMCreator struct {
	AgentConfig    *config.Agent
	ClientProvider kubecluster.ClientProvider
}

type Service struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ClusterIP string `json:"cluster_ip"`
	Ports     []Port `json:"ports"`
}

type Port struct {
	Name     string `json:"name"`
	NodePort int32  `json:"node_port"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type ResourceName string

const (
	ResourceCPU    ResourceName = "cpu"
	ResourceMemory ResourceName = "memory"
)

type ResourceList map[ResourceName]string

type Resources struct {
	Limits   ResourceList `json:"limits"`
	Requests ResourceList `json:"requests"`
}

type VMCloudProperties struct {
	Context   string    `json:"context"`
	Services  []Service `json:"services,omitempty"`
	Resources Resources `json:"resources,omitempty"`
}

func (v *VMCreator) Create(
	agentID string,
	stemcellCID cpi.StemcellCID,
	cloudProps VMCloudProperties,
	networks cpi.Networks,
	diskCIDs []cpi.DiskCID,
	env cpi.Environment,
) (cpi.VMCID, error) {

	// only one network is supported
	network, err := getNetwork(networks)
	if err != nil {
		return "", err
	}

	// create the client set
	client, err := v.ClientProvider.New(cloudProps.Context)
	if err != nil {
		return "", err
	}

	// create the target namespace if it doesn't already exist
	err = createNamespace(client.Core(), client.Namespace())
	if err != nil {
		return "", err
	}

	// NOTE: This is a workaround for the fake Clientset. This should be
	// removed once https://github.com/kubernetes/client-go/issues/48 is
	// resolved.
	ns := client.Namespace()
	instanceSettings, err := v.InstanceSettings(agentID, networks, env)
	if err != nil {
		return "", err
	}

	// create the config map
	_, err = createConfigMap(client.ConfigMaps(), ns, agentID, instanceSettings)
	if err != nil {
		return "", err
	}

	// create the service
	err = createServices(client.Services(), ns, agentID, cloudProps.Services)
	if err != nil {
		return "", err
	}

	// create the pod
	_, err = createPod(client.Pods(), ns, agentID, string(stemcellCID), *network, cloudProps.Resources)
	if err != nil {
		return "", err
	}

	return NewVMCID(client.Context(), agentID), nil
}

func getNetwork(networks cpi.Networks) (*cpi.Network, error) {
	switch len(networks) {
	case 0:
		return nil, errors.New("a network is required")
	case 1:
		for _, nw := range networks {
			return &nw, nil
		}
	default:
		return nil, errors.New("multiple networks not supported")
	}

	panic("unreachable")
}

func (v *VMCreator) InstanceSettings(agentID string, networks cpi.Networks, env cpi.Environment) (*agent.Settings, error) {
	agentNetworks := agent.Networks{}
	for name, cpiNetwork := range networks {
		agentNetwork := agent.Network{}
		if err := cpi.Remarshal(cpiNetwork, &agentNetwork); err != nil {
			return nil, err
		}
		agentNetwork.Preconfigured = true
		agentNetworks[name] = agentNetwork
	}

	settings := &agent.Settings{
		Blobstore:  v.AgentConfig.Blobstore,
		MessageBus: v.AgentConfig.MessageBus,
		NTPServers: v.AgentConfig.NTPServers,

		AgentID: agentID,
		VM:      agent.VM{Name: agentID},

		Env:      env,
		Networks: agentNetworks,
		Disks:    agent.Disks{},
	}
	return settings, nil
}

func createNamespace(coreClient core.CoreV1Interface, namespace string) error {
	_, err := coreClient.Namespaces().Get(namespace)
	if err == nil {
		return nil
	}

	_, err = coreClient.Namespaces().Create(&v1.Namespace{
		ObjectMeta: v1.ObjectMeta{Name: namespace},
	})
	if err == nil {
		return nil
	}

	if statusError, ok := err.(*kubeerrors.StatusError); ok {
		if statusError.Status().Reason == unversioned.StatusReasonAlreadyExists {
			return nil
		}
	}
	return err
}

func createConfigMap(configMapService core.ConfigMapInterface, ns, agentID string, instanceSettings *agent.Settings) (*v1.ConfigMap, error) {
	instanceJSON, err := json.Marshal(instanceSettings)
	if err != nil {
		return nil, err
	}

	return configMapService.Create(&v1.ConfigMap{
		ObjectMeta: v1.ObjectMeta{
			Name:      "agent-" + agentID,
			Namespace: ns,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
		},
		Data: map[string]string{
			"instance_settings": string(instanceJSON),
		},
	})
}

func createServices(serviceClient core.ServiceInterface, ns, agentID string, services []Service) error {
	for _, svc := range services {
		serviceType := v1.ServiceTypeClusterIP
		if svc.Type == "NodePort" {
			serviceType = v1.ServiceTypeNodePort
		}

		var ports []v1.ServicePort
		for _, port := range svc.Ports {
			port := v1.ServicePort{
				Name:     port.Name,
				Protocol: v1.Protocol(port.Protocol),
				Port:     port.Port,
				NodePort: port.NodePort,
			}
			ports = append(ports, port)
		}

		service := &v1.Service{
			ObjectMeta: v1.ObjectMeta{
				Name:      svc.Name,
				Namespace: ns,
				Labels: map[string]string{
					"bosh.cloudfoundry.org/agent-id": agentID,
				},
			},
			Spec: v1.ServiceSpec{
				Type:      serviceType,
				ClusterIP: svc.ClusterIP,
				Ports:     ports,
				Selector: map[string]string{
					"bosh.cloudfoundry.org/agent-id": agentID,
				},
			},
		}

		_, err := serviceClient.Create(service)
		if err != nil {
			return err
		}
	}

	return nil
}

func createPod(podClient core.PodInterface, ns, agentID, image string, network cpi.Network, resources Resources) (*v1.Pod, error) {
	trueValue := true
	rootUID := int64(0)

	annotations := map[string]string{}
	if len(network.IP) > 0 {
		annotations["bosh.cloudfoundry.org/ip-address"] = network.IP
	}

	resourceReqs, err := getPodResourceRequirements(resources)
	if err != nil {
		return nil, err
	}

	return podClient.Create(&v1.Pod{
		ObjectMeta: v1.ObjectMeta{
			Name:        "agent-" + agentID,
			Namespace:   ns,
			Annotations: annotations,
			Labels: map[string]string{
				"bosh.cloudfoundry.org/agent-id": agentID,
			},
		},
		Spec: v1.PodSpec{
			Hostname: agentID,
			Containers: []v1.Container{{
				Name:            "bosh-job",
				Image:           image,
				ImagePullPolicy: v1.PullAlways,
				Command:         []string{"/usr/sbin/runsvdir-start"},
				Args:            []string{},
				Resources:       resourceReqs,
				SecurityContext: &v1.SecurityContext{
					Privileged: &trueValue,
					RunAsUser:  &rootUID,
				},
				VolumeMounts: []v1.VolumeMount{{
					Name:      "bosh-config",
					MountPath: "/var/vcap/bosh/instance_settings.json",
					SubPath:   "instance_settings.json",
				}, {
					Name:      "bosh-ephemeral",
					MountPath: "/var/vcap/data",
				}},
			}},
			Volumes: []v1.Volume{{
				Name: "bosh-config",
				VolumeSource: v1.VolumeSource{
					ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{
							Name: "agent-" + agentID,
						},
						Items: []v1.KeyToPath{{
							Key:  "instance_settings",
							Path: "instance_settings.json",
						}},
					},
				},
			}, {
				Name: "bosh-ephemeral",
				VolumeSource: v1.VolumeSource{
					EmptyDir: &v1.EmptyDirVolumeSource{},
				},
			}},
		},
	})
}

func getPodResourceRequirements(resources Resources) (v1.ResourceRequirements, error) {
	limits, err := getResourceList(resources.Limits)
	if err != nil {
		return v1.ResourceRequirements{}, err
	}

	requests, err := getResourceList(resources.Requests)
	if err != nil {
		return v1.ResourceRequirements{}, err
	}

	return v1.ResourceRequirements{Limits: limits, Requests: requests}, nil
}

func getResourceList(resourceList ResourceList) (v1.ResourceList, error) {
	if resourceList == nil {
		return nil, nil
	}

	list := v1.ResourceList{}
	for k, v := range resourceList {
		quantity, err := resource.ParseQuantity(v)
		if err != nil {
			return nil, err
		}

		name, err := kubeResourceName(k)
		if err != nil {
			return nil, err
		}
		list[name] = quantity
	}

	return list, nil
}

func kubeResourceName(name ResourceName) (v1.ResourceName, error) {
	switch name {
	case ResourceMemory:
		return v1.ResourceMemory, nil
	case ResourceCPU:
		return v1.ResourceCPU, nil
	default:
		return "", fmt.Errorf("%s is not a supported resource type", name)
	}
}
