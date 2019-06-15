package pgproxy

import (
	"io"
	"log"
	"net"
	"strings"

	"github.com/rueian/pgbroker/backend"
	"github.com/rueian/pgbroker/message"
	"github.com/rueian/pgbroker/proxy"
)

func NewPGBroker(resolver backend.PGResolver) *proxy.Server {
	clientMessageHandlers := proxy.NewClientMessageHandlers()
	serverMessageHandlers := proxy.NewServerMessageHandlers()

	clientMessageHandlers.AddHandleQuery(func(ctx *proxy.Ctx, msg *message.Query) (query *message.Query, e error) {
		if c, ok := ctx.ServerConn.(*Conn); ok {
			c.StartHeartbeat()
		}

		user := ctx.ConnInfo.StartupParameters["user"]
		database := ctx.ConnInfo.StartupParameters["database"]
		log.Printf("Query: db=%s user=%s query=%s\n", database, user, strings.ReplaceAll(msg.QueryString, "\n", " "))
		return msg, nil
	})

	clientMessageHandlers.AddHandleParse(func(ctx *proxy.Ctx, msg *message.Parse) (parse *message.Parse, e error) {
		if c, ok := ctx.ServerConn.(*Conn); ok {
			c.StartHeartbeat()
		}

		user := ctx.ConnInfo.StartupParameters["user"]
		database := ctx.ConnInfo.StartupParameters["database"]
		log.Printf("Query: db=%s user=%s query=%s\n", database, user, strings.ReplaceAll(msg.QueryString, "\n", " "))
		return msg, nil
	})

	serverMessageHandlers.AddHandleReadyForQuery(func(ctx *proxy.Ctx, msg *message.ReadyForQuery) (query *message.ReadyForQuery, e error) {
		if c, ok := ctx.ServerConn.(*Conn); ok {
			c.StopHeartbeat()
		}
		return msg, nil
	})

	server := &proxy.Server{
		PGResolver:            resolver,
		ConnInfoStore:         backend.NewInMemoryConnInfoStore(),
		ServerMessageHandlers: serverMessageHandlers,
		ClientMessageHandlers: clientMessageHandlers,
		OnHandleConnError: func(err error, ctx *proxy.Ctx, conn net.Conn) {
			if err == io.EOF {
				return
			}

			client := conn.RemoteAddr().String()
			server := ""
			if ctx.ConnInfo.ServerAddress != nil {
				server = ctx.ConnInfo.ServerAddress.String()
			}
			user := ""
			database := ""
			if ctx.ConnInfo.StartupParameters != nil {
				user = ctx.ConnInfo.StartupParameters["user"]
				database = ctx.ConnInfo.StartupParameters["database"]
			}

			log.Printf("Error: client=%s server=%s user=%s db=%s err=%s", client, server, user, database, err.Error())
		},
	}

	return server
}
