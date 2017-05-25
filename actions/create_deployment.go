package actions

import (
	"errors"

	"github.com/ScarletTanager/kubernetes-cpi/cpi"
	"github.com/ScarletTanager/kubernetes-cpi/kubecluster"
	typed "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

type DeploymentCreator struct {
	// AgentConfig    *config.Agent
	ClientProvider kubecluster.ClientProvider
}

func (d *DeploymentCreator) Create(env cpi.Environment) error {
	_, err := createDeployment(nil, "ns")
	return err
}

func createDeployment(deploymentClient typed.DeploymentInterface, ns string) (*v1beta1.Deployment, error) {
	return nil, errors.New("TEST")
}
