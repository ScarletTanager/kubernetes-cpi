package actions_test

import (
	"github.com/ScarletTanager/kubernetes-cpi/actions"
	"github.com/ScarletTanager/kubernetes-cpi/cpi"
	"github.com/ScarletTanager/kubernetes-cpi/kubecluster/fakes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateDeployment", func() {
	var (
		fakeClient   *fakes.Client
		fakeProvider *fakes.ClientProvider

		env cpi.Environment

		deploymentCreator *actions.DeploymentCreator
	)

	BeforeEach(func() {
		fakeClient = fakes.NewClient()
		fakeClient.ContextReturns("bosh")
		fakeClient.NamespaceReturns("bosh-namespaces")

		fakeProvider = &fakes.ClientProvider{}
		fakeProvider.NewReturns(fakeClient, nil)

		deploymentCreator = &actions.DeploymentCreator{
			ClientProvider: fakeProvider,
		}

		env = cpi.Environment{"passed": "along"}
	})

	Describe("Create", func() {
		It("Creates a simple deployment", func() {
			Expect(deploymentCreator.Create(env)).NotTo(HaveOccurred())
		})
	})

})
