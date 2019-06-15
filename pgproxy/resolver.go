package pgproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/rueian/godemand/client"
	"github.com/rueian/godemand/types"
)

var DatabaseMap = map[string]string{
	"db1": "pg10",
	"db2": "pg11",
}

type GodemandResolver struct {
	Host        string
	DatabaseMap map[string]string
}

func (r *GodemandResolver) GetPGConn(ctx context.Context, clientAddr net.Addr, parameters map[string]string) (net.Conn, error) {
	database := parameters["database"]
	user := parameters["user"]

	pool, ok := r.DatabaseMap[database]
	if !ok {
		return nil, errors.New("database " + database + " is not supported by godemand")
	}

	c := client.NewHTTPClient(r.Host, types.Client{
		ID: clientAddr.String(),
		Meta: map[string]interface{}{
			"user":     user,
			"database": database,
		},
	}, http.DefaultClient)

	res, err := c.RequestResource(ctx, pool)
	if err != nil {
		return nil, err
	}

	if addr, ok := res.Meta["addr"]; ok {
		conn, err := net.Dial("tcp", addr.(string))
		if err != nil {
			return nil, err
		}
		wrapConn := WrapConn(conn.(*net.TCPConn), res, c)
		go wrapConn.Heartbeat()

		return wrapConn, nil
	}

	return nil, errors.New("resource doesn't include the ip addr")
}

func WrapConn(conn *net.TCPConn, resource types.Resource, client *client.HTTPClient) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		TCPConn:  conn,
		resource: resource,
		client:   client,
		ctx:      ctx,
		cancel:   cancel,
	}
}

type Conn struct {
	*net.TCPConn
	resource types.Resource
	client   *client.HTTPClient

	ctx         context.Context
	cancel      context.CancelFunc
	heartbeat   bool
	heartbeatAt time.Time
}

func (c *Conn) Close() error {
	c.cancel()
	return c.TCPConn.Close()
}

func (c *Conn) Heartbeat() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		if c.heartbeat {
			c.heartbeatAt = time.Now()
			c.client.Heartbeat(c.ctx, c.resource)
		}
		time.Sleep(10 * time.Second)
	}
}

func (c *Conn) StartHeartbeat() {
	c.heartbeat = true
}

func (c *Conn) StopHeartbeat() {
	c.heartbeat = false
	go func() {
		if time.Since(c.heartbeatAt) > 10*time.Second {
			c.heartbeatAt = time.Now()
			c.client.Heartbeat(c.ctx, c.resource)
		}
	}()
}
