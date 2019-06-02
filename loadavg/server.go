package main

import (
	"io"
	"log"
	"net"
	"os"
)

func main() {
	ln, err := net.Listen("tcp", ":8743")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go func() {
			defer conn.Close()

			loadavg, err := os.Open("/proc/loadavg")
			if err == nil {
				defer loadavg.Close()
				_, err = io.Copy(conn, loadavg)
			}
			if err != nil {
				log.Println("fail to export loadavg", err.Error())
			}
		}()
	}
}
