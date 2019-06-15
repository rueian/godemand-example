package pgplugin

import (
	"bytes"
	"fmt"
	"log"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/rueian/godemand-example/tools"
	"github.com/rueian/godemand/types"
	"google.golang.org/api/compute/v1"
)

type CallParam struct {
	MaxLoads          int
	MaxLifeSecond     int
	MaxServSecond     int
	MaxIdleSecond     int
	MaxSyncWindow     int
	SnapshotPrefix    string
	SnapshotProjectID string
	InstanceProjectID string
	InstanceZone      string
	InstanceMachine   string
}

type SnapshotCache struct {
	Snapshot string
	FoundAt  time.Time
}

type Controller struct {
	Service          *tools.ComputeService
	StartupFactory   func(params map[string]interface{}, snapshot string) StartupParam
	CallParamFactory func(params map[string]interface{}) CallParam
	LatestSnapshots  map[string]SnapshotCache
}

var StateOrder = map[types.ResourceState]int{
	types.ResourceServing:     0,
	types.ResourceBooting:     1,
	types.ResourceTerminated:  2,
	types.ResourceTerminating: 3,
	types.ResourcePending:     4,
	types.ResourceDeleting:    99,
	types.ResourceDeleted:     99,
	types.ResourceUnknown:     99,
	types.ResourceError:       99,
}

func (c *Controller) FindResource(pool types.ResourcePool, params map[string]interface{}) (types.Resource, error) {
	cp := c.CallParamFactory(params)

	var resources []types.Resource

	for _, res := range pool.Resources {
		if StateOrder[res.State] < 99 {
			resources = append(resources, res)
		}
	}

	sort.Slice(resources, func(i, j int) bool {
		if resources[i].State == resources[j].State {
			if resources[i].State == types.ResourceServing {
				return resources[i].StateChange.After(resources[j].StateChange)
			} else {
				return resources[i].StateChange.Before(resources[j].StateChange)
			}
		}
		return StateOrder[resources[i].State] < StateOrder[resources[j].State]
	})

	for _, res := range resources {
		if time.Since(res.CreatedAt) > time.Duration(cp.MaxLifeSecond)*time.Second {
			k := cp.SnapshotProjectID + cp.SnapshotPrefix
			if c.LatestSnapshots[k].FoundAt.Before(res.CreatedAt) {
				snapshot, err := c.Service.FindLatestSnapshot(cp.SnapshotProjectID, cp.SnapshotPrefix)
				if err == nil {
					c.LatestSnapshots[k] = SnapshotCache{
						Snapshot: snapshot.SelfLink,
						FoundAt:  time.Now(),
					}
				}
			}
			if snapshot, ok := res.Meta["snapshot"].(string); ok && snapshot < c.LatestSnapshots[k].Snapshot {
				continue
			}
		}

		if loadAddr, ok := res.Meta["load"].(string); ok && res.State == types.ResourceServing {
			m1, m5, m15, err := tools.GetLoad(loadAddr)
			if err == nil && m1 > m5 && m1 > m15 && m1 > float64(cp.MaxLoads) {
				continue
			}
		}

		if res.State == types.ResourceTerminated || res.State == types.ResourceTerminating {
			res.State = types.ResourceBooting
		}
		return res, nil
	}

	return types.Resource{
		ID:        "godemand-" + cp.SnapshotPrefix + "-" + time.Now().Format("20060102150405"),
		PoolID:    pool.ID,
		State:     types.ResourcePending,
		CreatedAt: time.Now(),
	}, nil
}

func (c *Controller) SyncResource(resource types.Resource, params map[string]interface{}) (types.Resource, error) {
	cp := c.CallParamFactory(params)

	switch resource.State {
	case types.ResourcePending:
		found, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if found != nil {
			resource.State = types.ResourceBooting
			resource.LastSynced = time.Now()
			return resource, nil
		}
		if err != nil && !tools.IsStatusNotFound(err) {
			log.Printf("fail to find instance %q due to google api error: %s\n", cp.SnapshotPrefix, err.Error())
			return types.Resource{}, err
		}

		var d *compute.Disk
		d, err = c.Service.FindDisk(cp.InstanceProjectID, cp.InstanceZone, resource.ID)
		if tools.IsStatusNotFound(err) {
			snapshot, err := c.Service.FindLatestSnapshot(cp.SnapshotProjectID, cp.SnapshotPrefix)
			if err != nil {
				log.Printf("fail to find latest snapshot of prefix %q: %s\n", cp.SnapshotPrefix, err.Error())
				return types.Resource{}, err
			}
			c.LatestSnapshots[cp.SnapshotProjectID+cp.SnapshotPrefix] = SnapshotCache{
				Snapshot: snapshot.SelfLink,
				FoundAt:  time.Now(),
			}

			if snapshot == nil {
				log.Printf("no snapshot found of prefix %q, mark deleted\n", cp.SnapshotPrefix)
				resource.State = types.ResourceDeleted
				resource.LastSynced = time.Now()
				return resource, nil
			}
			err = c.Service.CreateDiskRetry(cp.InstanceProjectID, cp.InstanceZone, makeDisk(resource.ID, cp.SnapshotPrefix, snapshot.SelfLink), 5)
			if err != nil {
				log.Printf("fail to create disk of snapshot %q: %s\n", snapshot.Name, err.Error())
				return types.Resource{}, err
			}
		}
		if d == nil || err != nil {
			d, err = c.Service.FindDiskRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
			if err != nil {
				log.Printf("fail to find disk %q: %s\n", resource.ID, err.Error())
				return types.Resource{}, err
			}
		}

		if d.Status == "CREATING" || d.Status == "RESTORING" {
			log.Printf("wait disk %q to be ready, got %q\n", d.Name, d.Status)
			return types.Resource{}, fmt.Errorf("wait disk %q to be ready, got %q", d.Name, d.Status)
		}
		if d.Status == "FAILED" {
			log.Printf("deleting the failed disk %q\n", resource.ID)
			c.Service.DeleteDiskRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
			return types.Resource{}, fmt.Errorf("disk %q status %q", d.Name, d.Status)
		}

		if len(d.Users) > 0 {
			err = fmt.Errorf("disk is used by instances: %v", d.Users)
			log.Printf("fail to create instance %q: %s\n", resource.ID, err.Error())
			return types.Resource{}, err
		}

		err = c.Service.CreateInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, makeInstance(resource.ID, cp.InstanceProjectID, cp.InstanceZone, cp.InstanceMachine, d, cp.SnapshotPrefix, params, c.StartupFactory), 5)
		if err != nil {
			log.Printf("fail to create instance %q: %s\n", resource.ID, err.Error())
			return types.Resource{}, err
		}

		log.Printf("instance %q created\n", resource.ID)
		resource.Meta = types.Meta{
			"snapshot": d.SourceSnapshot,
		}
		resource.State = types.ResourceBooting
	case types.ResourceBooting:
		// check service running
		instance, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if err != nil && tools.IsStatusNotFound(err) {
			log.Printf("instance %q disappeared, mark deleted\n", resource.ID)
			resource.State = types.ResourceDeleted
			break
		}

		switch instance.Status {
		case "RUNNING":
			if success, err := tools.Poke(instance, "8743", 5); success {
				resource.State = types.ResourceServing
				resource.Meta = types.Meta{
					"addr":     instance.NetworkInterfaces[0].NetworkIP + ":5432",
					"load":     instance.NetworkInterfaces[0].NetworkIP + ":8743",
					"snapshot": resource.Meta["snapshot"],
				}
			} else if err != nil {
				log.Printf("fail to poke instance %q on startup port, try again later: %s\n", resource.ID, err.Error())
			}
		case "STOPPED", "TERMINATED":
			if err := c.Service.StartInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5); err != nil {
				log.Printf("fail to start instance %q, try again later: %s\n", resource.ID, err.Error())
			}
		}

		if resource.StateChange.Add(2 * time.Duration(cp.MaxIdleSecond) * time.Second).Before(time.Now()) {
			log.Printf("instance %q createing exceeds 2x MaxIdleSecond %d, mark deleting\n", resource.ID, 2*cp.MaxIdleSecond)
			resource.State = types.ResourceDeleting
		}
	case types.ResourceServing:
		if resource.LastSynced.Add(time.Duration(cp.MaxSyncWindow) * time.Second).After(time.Now()) {
			return resource, nil
		}
		// check service running
		instance, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if err != nil && tools.IsStatusNotFound(err) {
			log.Printf("instance %q disappeared, mark deleted\n", resource.ID)
			resource.State = types.ResourceDeleted
			break
		}
		if instance.Status == "STOPPING" {
			log.Printf("instance %q stopping, mark terminating\n", resource.ID)
			resource.State = types.ResourceTerminating
			break
		}
		if instance.Status == "TERMINATED" {
			log.Printf("instance %q stopped, mark termintated\n", resource.ID)
			resource.State = types.ResourceTerminated
			break
		}
		if instance.Status == "PROVISIONING" {
			log.Printf("instance %q provisioning, mark booting\n", resource.ID)
			resource.State = types.ResourceBooting
			break
		}
		if instance.Status == "STAGING" {
			log.Printf("instance %q staging, mark booting\n", resource.ID)
			resource.State = types.ResourceBooting
			break
		}
		if instance.Status != "RUNNING" {
			log.Printf("instance %q not running but %s, mark deleting\n", instance.Status, resource.ID)
			resource.State = types.ResourceDeleting
			break
		}
		ts := resource.LastClientHeartbeat
		if ts.Before(resource.StateChange) {
			ts = resource.StateChange
		}
		if ts.Add(time.Duration(cp.MaxIdleSecond) * time.Second).Before(time.Now()) {
			log.Printf("instance %q exceeds MaxIdleSecond %d, mark terminating\n", resource.ID, cp.MaxIdleSecond)
			resource.State = types.ResourceTerminating
			break
		}
		if resource.CreatedAt.Add(time.Duration(cp.MaxServSecond) * time.Second).Before(time.Now()) {
			log.Printf("instance %q exceeds MaxServSecond %d, mark deleting\n", resource.ID, cp.MaxServSecond)
			resource.State = types.ResourceDeleting
			break
		}
		if _, err := tools.Poke(instance, "5432", 5); err != nil {
			log.Printf("fail to poke instance %q, mark terminating: %s\n", resource.ID, err.Error())
			resource.State = types.ResourceTerminating
			break
		}

	case types.ResourceTerminating:
		instance, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if err != nil && tools.IsStatusNotFound(err) {
			log.Printf("instance %q disappeared, mark deleted\n", resource.ID)
			resource.State = types.ResourceDeleted
			break
		}
		if instance.Status == "RUNNING" {
			if err := c.Service.TerminateInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5); err != nil {
				log.Printf("fail to stop instance %q, try again later: %s\n", resource.ID, err.Error())
			}
		} else if instance.Status == "TERMINATED" {
			log.Printf("instance %q stopped, mark terminated\n", resource.ID)
			resource.State = types.ResourceTerminated
		}
	case types.ResourceTerminated:
		if resource.LastSynced.Add(time.Duration(cp.MaxSyncWindow) * time.Second).After(time.Now()) {
			return resource, nil
		}

		instance, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if err != nil && tools.IsStatusNotFound(err) {
			log.Printf("instance %q disappeared, mark deleted\n", resource.ID)
			resource.State = types.ResourceDeleted
			break
		}
		if resource.CreatedAt.Add(time.Duration(cp.MaxLifeSecond) * time.Second).Before(time.Now()) {
			k := cp.SnapshotProjectID + cp.SnapshotPrefix
			if c.LatestSnapshots[k].FoundAt.Before(resource.CreatedAt) {
				snapshot, err := c.Service.FindLatestSnapshot(cp.SnapshotProjectID, cp.SnapshotPrefix)
				if err == nil {
					c.LatestSnapshots[k] = SnapshotCache{
						Snapshot: snapshot.SelfLink,
						FoundAt:  time.Now(),
					}
				}
			}
			if snapshot, ok := resource.Meta["snapshot"].(string); ok && snapshot < c.LatestSnapshots[k].Snapshot {
				log.Printf("instance %q exceeds MaxLifeSecond %d, mark deleting\n", resource.ID, cp.MaxLifeSecond)
				resource.State = types.ResourceDeleting
				break
			}
		}

		if instance.Status == "RUNNING" {
			log.Printf("instance %q running again, mark booting\n", resource.ID)
			resource.State = types.ResourceBooting
		}
	case types.ResourceDeleting:
		// if instance not found
		_, err := c.Service.FindInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5)
		if err != nil && tools.IsStatusNotFound(err) {
			log.Printf("instance %q disappeared, mark deleted\n", resource.ID)
			resource.State = types.ResourceDeleted
			break
		}
		if err := c.Service.DeleteInstanceRetry(cp.InstanceProjectID, cp.InstanceZone, resource.ID, 5); err != nil {
			log.Printf("fail to delete instance %q: %v", resource.ID, err)
		} else {
			resource.State = types.ResourceDeleted
			log.Printf("instance %q deleted\n", resource.ID)
		}
	case types.ResourceDeleted:
		// skip
	}

	resource.LastSynced = time.Now()

	return resource, nil
}

func makeDisk(name, snapshotPrefix, snapshotLink string) *compute.Disk {
	return &compute.Disk{
		Name:           name,
		SourceSnapshot: snapshotLink,
		Labels: map[string]string{
			"godemand": snapshotPrefix,
		},
	}
}

func makeInstance(name, projectID, zone, machineType string, disk *compute.Disk, snapshotPrefix string, params map[string]interface{}, factory func(params map[string]interface{}, snapshot string) StartupParam) *compute.Instance {
	buf := &bytes.Buffer{}
	startup.Execute(buf, factory(params, disk.SourceSnapshot))
	script := buf.String()

	zs := strings.Split(zone, "-")
	region := strings.Join(zs[:2], "-")

	return &compute.Instance{
		Name: name,
		Labels: map[string]string{
			"godemand": snapshotPrefix,
		},
		MachineType:  "zones/" + zone + "/machineTypes/" + machineType,
		CanIpForward: true,
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Network:    "projects/" + projectID + "/global/networks/default",
				Subnetwork: "regions/" + region + "/subnetworks/default",
				AccessConfigs: []*compute.AccessConfig{
					{
						Type: "ONE_TO_ONE_NAT",
					},
				},
			},
		},
		Disks: []*compute.AttachedDisk{
			{
				Boot:       true,
				AutoDelete: true,
				Source:     disk.SelfLink,
			},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "startup-script",
					Value: &script,
				},
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible: true,
		},
	}
}

type StartupParam struct {
	ConfigPath         string
	HbaPath            string
	TriggerPath        string
	RecoveryConfigPath string
	SnapshotSource     string
}

var startup = template.Must(template.New("startup").Parse(`#!/bin/bash -e

total_mem=$(expr $(cat /proc/meminfo | grep MemTotal | awk '{print $2}') )
hugepage_size=$(expr $(cat /proc/meminfo | grep Hugepagesize | awk '{print $2}') )
cpu_count=$(cat /proc/cpuinfo | grep processor | wc -l)
max_workers=$(expr $cpu_count \* 2 + 2)

shared_buffers=$(expr $total_mem \/ 4)
hugepages_mem=$(expr $total_mem \/ 3)
effective_cache_size=$(expr $total_mem \* 3 \/ 4)
maintenance_work_mem=$(expr $total_mem \/ 16)
work_mem=$(expr $total_mem \/ 4 \/ 100)
nr_hugepages=$(expr $hugepages_mem \/ $hugepage_size + 1)

echo $nr_hugepages > /proc/sys/vm/nr_hugepages
echo "never" > /sys/kernel/mm/transparent_hugepage/enabled

echo "listen_addresses = '*'" >> {{ .ConfigPath }}

sed -i "s/^max_connections = .*/max_connections = 400/g" {{ .ConfigPath }}
sed -i "s/^shared_buffers = .*/shared_buffers = $(expr $shared_buffers \/ 1024)MB/g" {{ .ConfigPath }}
sed -i "s/^effective_cache_size = .*/effective_cache_size = $(expr $effective_cache_size \/ 1024)MB/g" {{ .ConfigPath }}
sed -i "s/^maintenance_work_mem = .*/maintenance_work_mem = $(expr $maintenance_work_mem \/ 1024)MB/g" {{ .ConfigPath }}
sed -i "s/^work_mem = .*/work_mem = $(echo $work_mem)kB/g" {{ .ConfigPath }}

sed -i "s/^max_worker_processes = .*/max_worker_processes = $(echo $max_workers)/g" {{ .ConfigPath }}
sed -i "s/^max_parallel_workers_per_gather = .*/max_parallel_workers_per_gather = $(echo $cpu_count)/g" {{ .ConfigPath }}

echo "random_page_cost = 6" >> {{ .ConfigPath }}

grep -Fxq "host all all 10.0.0.0/8 md5" {{ .HbaPath }} || echo "host all all 10.0.0.0/8 md5" >> {{ .HbaPath }}
grep -Fxq "host all all 172.16.0.0/12 md5" {{ .HbaPath }} || echo "host all all 172.16.0.0/12 md5" >> {{ .HbaPath }}
grep -Fxq "host all all 192.168.0.0/16 md5" {{ .HbaPath }} || echo "host all all 192.168.0.0/16 md5" >> {{ .HbaPath }}

rm {{ .RecoveryConfigPath }} || true

service postgresql restart

touch {{ .TriggerPath }}
chown postgres:postgres {{ .TriggerPath }}

until eval 'sudo -u postgres psql -c "create table if not exists godemand ( snapshot text PRIMARY KEY, boot_at timestamp with time zone default current_timestamp )"'
do
  sleep 1
done

until eval 'sudo -u postgres psql -c "insert into godemand (snapshot) values ('\''{{ .SnapshotSource }}'\'') on conflict do nothing"'
do
  sleep 1
done

until eval 'sudo -u postgres psql -c "create database db1"'
do
  sleep 1
done

until eval 'sudo -u postgres psql -c "create database db2"'
do
  sleep 1
done

service loadavg start
`))
