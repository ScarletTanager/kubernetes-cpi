package actions

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"code.cloudfoundry.org/clock"

	"github.com/ScarletTanager/kubernetes-cpi/cpi"
	"github.com/ScarletTanager/kubernetes-cpi/kubecluster"
	core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/pkg/api/resource"
	"k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/labels"
	"k8s.io/client-go/pkg/watch"
)

type CreateDiskCloudProperties struct {
	Context string `json:"context"`
}

// DiskCreator simply creates a PersistentVolumeClaim.
// True? --> The attach process will
// turn the claim into a volume mounted into the pod.
type DiskCreator struct {
	ClientProvider    kubecluster.ClientProvider
	Clock             clock.Clock
	DiskReadyTimeout  time.Duration
	GUIDGeneratorFunc func() (string, error)
}

func (d *DiskCreator) CreateDisk(size uint, cloudProps CreateDiskCloudProperties, vmcid cpi.VMCID) (cpi.DiskCID, error) {
	diskID, err := d.GUIDGeneratorFunc()
	if err != nil {
		return "", err
	}

	volumeSize, err := resource.ParseQuantity(fmt.Sprintf("%dGi", size))
	if err != nil {
		return "", err
	}

	client, err := d.ClientProvider.New(cloudProps.Context)
	if err != nil {
		return "", err
	}

	// volumeName := "volume-" + diskID

	// _, err = client.PersistentVolumes().Create(&v1.PersistentVolume{
	// 	ObjectMeta: v1.ObjectMeta{
	// 		Name: volumeName,
	// 		Labels: map[string]string{
	// 			"bosh.cloudfoundry.org/disk-id": diskID,
	// 		},
	// 	},
	// 	Spec: v1.PersistentVolumeSpec{
	// 		AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
	// 		Capacity: v1.ResourceList{
	// 			v1.ResourceStorage: volumeSize,
	// 		},
	// 		PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimRecycle,
	// 	},
	// })

	// if err != nil {
	// 	return "", err
	// }

	pvc, err := client.PersistentVolumeClaims().Create(&v1.PersistentVolumeClaim{
		ObjectMeta: v1.ObjectMeta{
			Name:      "disk-" + diskID,
			Namespace: client.Namespace(),
			Annotations: map[string]string{
				"volume.beta.kubernetes.io/storage-class":       "ibmc-file-gold",
				"volume.beta.kubernetes.io/storage-provisioner": "ibm.io/ibmc-file",
			},
			Labels: map[string]string{
				"bosh.cloudfoundry.org/disk-id": diskID,
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			// VolumeName:  volumeName,
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceStorage: volumeSize,
				},
			},
		},
	})

	if err != nil {
		return "", err
	}

	ready, err := d.waitForDisk(client.PersistentVolumeClaims(), diskID, pvc.ResourceVersion)

	if err != nil {
		return "", err
	}

	if !ready {
		return "", errors.New("Disk creation failed with a timeout")
	}
	return NewDiskCID(client.Context(), diskID), nil
}

func (d *DiskCreator) waitForDisk(pvcService core.PersistentVolumeClaimInterface, diskID string, resourceVersion string) (bool, error) {
	diskSelector, err := labels.Parse("bosh.cloudfoundry.org/disk-id=" + diskID)
	if err != nil {
		return false, err
	}

	listOptions := v1.ListOptions{
		LabelSelector:   diskSelector.String(),
		ResourceVersion: resourceVersion,
		Watch:           true,
	}

	timer := d.Clock.NewTimer(d.DiskReadyTimeout)
	defer timer.Stop()

	pvcWatch, err := pvcService.Watch(listOptions)
	if err != nil {
		return false, err
	}
	defer pvcWatch.Stop()

	for {
		select {
		case event := <-pvcWatch.ResultChan():
			switch event.Type {
			case watch.Modified:
				pvc, ok := event.Object.(*v1.PersistentVolumeClaim)
				if !ok {
					return false, fmt.Errorf("Unexpected object type: %v", reflect.TypeOf(event.Object))
				}

				if isDiskReady(pvc) {
					return true, nil
				}

			case watch.Added:
				pvc, ok := event.Object.(*v1.PersistentVolumeClaim)
				if !ok {
					return false, fmt.Errorf("Unexpected object type: %v", reflect.TypeOf(event.Object))
				}

				if isDiskReady(pvc) {
					return true, nil
				}

			case watch.Error:
				return false, fmt.Errorf("Received an error when provisioning pvc: %s", event.Object.GetObjectKind().GroupVersionKind().Kind)

			case watch.Deleted:
				return false, errors.New("Unexpected deletion event when provisioning pvc")

			default:
				return false, fmt.Errorf("Unexpected pvc watch event: %v Object: %v Type: %s", reflect.TypeOf(event), reflect.TypeOf(event.Object), event.Type)
				//return true, nil
			}

		case <-timer.C():
			return false, nil
		}
	}
}

func isDiskReady(pvc *v1.PersistentVolumeClaim) bool {
	if pvc.Status.Phase != v1.ClaimBound {
		return false
	}

	return true
}
