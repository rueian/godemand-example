package tools

import (
	"errors"
	"net"
	"time"

	"cloud.google.com/go/compute/metadata"
	"google.golang.org/api/compute/v1"
)

func Poke(instance *compute.Instance, port string, times int) (bool, error) {
	var err error
	var conn net.Conn

	if len(instance.NetworkInterfaces) == 0 {
		return false, errors.New("no network interface")
	}

	for i := 0; i < times; i++ {
		for _, n := range instance.NetworkInterfaces {
			time.Sleep(time.Second)
			if metadata.OnGCE() {
				if n.NetworkIP == "" {
					continue
				}
				if conn, err = net.DialTimeout("tcp", n.NetworkIP+":"+port, 1*time.Second); conn != nil {
					conn.Close()
					return true, nil
				}
			} else {
				for _, a := range n.AccessConfigs {
					if a.NatIP == "" {
						continue
					}
					if conn, err = net.DialTimeout("tcp", a.NatIP+":"+port, 1*time.Second); conn != nil {
						conn.Close()
						return true, nil
					}
				}
			}
		}
	}
	if err == nil {
		err = errors.New("no network ip")
	}
	return false, err
}
