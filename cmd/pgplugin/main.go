package main

import (
	"context"
	"log"

	"cloud.google.com/go/compute/metadata"
	"github.com/rueian/godemand-example/pgplugin"
	"github.com/rueian/godemand-example/tools"
	"github.com/rueian/godemand/plugin"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

func StartParam(params map[string]interface{}, snapshot string) pgplugin.StartupParam {
	return pgplugin.StartupParam{
		ConfigPath:         tools.GetStr(params, "ConfigPath", "/etc/postgresql/11/main/postgresql.conf"),
		HbaPath:            tools.GetStr(params, "HbaPath", "/etc/postgresql/11/main/pg_hba.conf"),
		RecoveryConfigPath: tools.GetStr(params, "RecoveryConfigPath", "/var/lib/postgresql/11/main/recovery.conf"),
		TriggerPath:        tools.GetStr(params, "TriggerPath", "/tmp/postgresql.trigger.5432"),
		SnapshotSource:     snapshot,
	}
}

func CallParam(params map[string]interface{}) pgplugin.CallParam {
	projectID, _ := metadata.ProjectID()

	return pgplugin.CallParam{
		MaxLoads:          tools.GetInt(params, "MaxLoads", 10),
		MaxServSecond:     tools.GetInt(params, "MaxServSecond", 10800),
		MaxLifeSecond:     tools.GetInt(params, "MaxLifeSecond", 1800),
		MaxIdleSecond:     tools.GetInt(params, "MaxIdleSecond", 300),
		MaxSyncWindow:     tools.GetInt(params, "MaxSyncWindow", 30),
		SnapshotPrefix:    tools.GetStr(params, "SnapshotPrefix", "pg11"),
		SnapshotProjectID: tools.GetStr(params, "SnapshotProjectID", projectID),
		InstanceProjectID: tools.GetStr(params, "InstanceProjectID", projectID),
		InstanceZone:      tools.GetStr(params, "InstanceZone", "us-west1-a"),
		InstanceMachine:   tools.GetStr(params, "InstanceMachine", "f1-micro"),
	}
}

func main() {
	// remove timestamp from plugin logging because godemand will log it.
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	ctx := context.Background()

	cred, err := google.FindDefaultCredentials(ctx, compute.ComputeScope)
	if err != nil {
		panic(err)
	}

	service, err := compute.NewService(ctx, option.WithCredentials(cred))
	if err != nil {
		panic(err)
	}

	controller := &pgplugin.Controller{
		Service:          tools.NewComputeService(service),
		StartupFactory:   StartParam,
		CallParamFactory: CallParam,
		LatestSnapshots:  make(map[string]pgplugin.SnapshotCache),
	}

	if err := plugin.Serve(ctx, controller); err != nil {
		log.Fatal(err)
	}
}
