package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"contrib.go.opencensus.io/exporter/stackdriver"
	goredis "github.com/go-redis/redis"
	"github.com/rueian/godemand/api"
	"github.com/rueian/godemand/config"
	"github.com/rueian/godemand/metrics"
	"github.com/rueian/godemand/plugin"
	"github.com/rueian/godemand/redis"
	"github.com/rueian/godemand/syncer"
)

func main() {
	cfg, err := config.LoadConfig(os.Getenv("CONFIG_PATH"))
	if err != nil {
		log.Fatal(err)
	}

	client := goredis.NewUniversalClient(&goredis.UniversalOptions{
		Addrs: []string{os.Getenv("REDIS_ARRD")},
	})
	defer client.Close()

	sd, err := stackdriver.NewExporter(stackdriver.Options{})
	if err != nil {
		log.Fatalf("Failed to create the Stackdriver exporter: %v", err)
	}
	defer sd.Flush()

	go func() {
		err := metrics.StartRecording(2*time.Minute, sd)
		if err != nil {
			log.Fatalf("Failed StartRecording: %v", err)
		}
	}()

	pool := redis.NewResourcePool(client)
	locker := redis.NewLocker(client)
	launchpad := plugin.NewLaunchpad()
	defer launchpad.Close()

	err = launchpad.SetLaunchers(cfg.GetPluginCmd())
	if err != nil {
		log.Fatal(err)
	}

	service := &api.Service{
		Pool:      pool,
		Locker:    locker,
		Launchpad: launchpad,
		Config:    cfg,
	}

	syncer := &syncer.ResourceSyncer{
		Pool:      pool,
		Locker:    locker,
		Launchpad: launchpad,
		Config:    cfg,
	}

	go func() {
		for {
			time.Sleep(5 * time.Second)

			cfg, err := config.LoadConfig(os.Getenv("CONFIG_PATH"))
			if err != nil {
				continue
			}
			service.Config = cfg
			syncer.Config = cfg
			if err = launchpad.SetLaunchers(cfg.GetPluginCmd()); err != nil {
				continue
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		syncer.Run(ctx, 1)
	}()

	server := &http.Server{
		Addr:    ":8080",
		Handler: api.NewHTTPMux(service),
	}

	go func() {
		<-sigs
		cancel()
		ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
		server.Shutdown(ctx)
	}()

	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
