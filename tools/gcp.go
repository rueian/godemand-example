package tools

import (
	"net/http"
	"sort"
	"time"

	uuid "github.com/satori/go.uuid"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func NewComputeService(service *compute.Service) *ComputeService {
	return &ComputeService{
		DisksService:     compute.NewDisksService(service),
		SnapshotsService: compute.NewSnapshotsService(service),
		InstancesService: compute.NewInstancesService(service),
	}
}

type ComputeService struct {
	DisksService     *compute.DisksService
	SnapshotsService *compute.SnapshotsService
	InstancesService *compute.InstancesService
}

func (s *ComputeService) FindLatestSnapshot(projectID, prefix string) (*compute.Snapshot, error) {
	list, err := s.SnapshotsService.List(projectID).Filter(`(name = "` + prefix + `*") AND (status = "READY")`).Do()
	if err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	// gcp api does not support OrderBy with Filter, therefore we find the latest snapshot by ourselves.
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].CreationTimestamp > list.Items[j].CreationTimestamp })

	return list.Items[0], nil
}

func (s *ComputeService) FindInstance(projectID, zoneID, instanceID string) (instance *compute.Instance, err error) {
	instance, err = s.InstancesService.Get(projectID, zoneID, instanceID).Do()
	return
}

func (s *ComputeService) FindInstanceRetry(projectID, zoneID, instanceID string, times int) (instance *compute.Instance, err error) {
	for i := 0; i < times; i++ {
		if instance, err = s.FindInstance(projectID, zoneID, instanceID); instance != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) CreateDiskRetry(projectID, zoneID string, disk *compute.Disk, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.DisksService.Insert(projectID, zoneID, disk).RequestId(id).Do(); err == nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) CreateInstanceRetry(projectID, zoneID string, instance *compute.Instance, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.InstancesService.Insert(projectID, zoneID, instance).RequestId(id).Do(); err == nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) DeleteInstanceRetry(projectID, zoneID, instanceID string, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.InstancesService.Delete(projectID, zoneID, instanceID).RequestId(id).Do(); err == nil {
			return
		} else if IsStatusNotFound(err) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) TerminateInstanceRetry(projectID, zoneID, instanceID string, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.InstancesService.Stop(projectID, zoneID, instanceID).RequestId(id).Do(); err == nil {
			return
		} else if IsStatusNotFound(err) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) StartInstanceRetry(projectID, zoneID, instanceID string, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.InstancesService.Start(projectID, zoneID, instanceID).RequestId(id).Do(); err == nil {
			return
		} else if IsStatusNotFound(err) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) FindDisk(projectID, zoneID, diskID string) (disk *compute.Disk, err error) {
	disk, err = s.DisksService.Get(projectID, zoneID, diskID).Do()
	return
}

func (s *ComputeService) FindDiskRetry(projectID, zoneID, diskID string, times int) (disk *compute.Disk, err error) {
	for i := 0; i < times; i++ {
		if disk, err = s.FindDisk(projectID, zoneID, diskID); disk != nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func (s *ComputeService) DeleteDiskRetry(projectID, zoneID, diskID string, times int) (err error) {
	id := uuid.NewV4().String()
	for i := 0; i < times; i++ {
		if _, err = s.DisksService.Delete(projectID, zoneID, diskID).RequestId(id).Do(); err == nil {
			return
		} else if IsStatusNotFound(err) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return
}

func IsStatusNotFound(err error) bool {
	if e, ok := err.(*googleapi.Error); ok {
		if e.Code == http.StatusNotFound {
			return true
		}
	}
	return false
}
