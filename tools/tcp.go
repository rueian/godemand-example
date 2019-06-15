package tools

import (
	"errors"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
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

func GetLoad(addr string) (float64, float64, float64, error) {
	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		return 0, 0, 0, err
	}
	defer conn.Close()

	output, err := ioutil.ReadAll(conn)
	if err != nil {
		return 0, 0, 0, err
	}

	loading := strings.Split(string(output), " ")
	if len(loading) < 3 {
		return 0, 0, 0, errors.New("malformed loadavg")
	}

	m1, err := strconv.ParseFloat(loading[0], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	m5, err := strconv.ParseFloat(loading[1], 64)
	if err != nil {
		return 0, 0, 0, err
	}
	m15, err := strconv.ParseFloat(loading[2], 64)
	if err != nil {
		return 0, 0, 0, err
	}

	return m1, m5, m15, nil
}
