// Command mqttlocal-sub is a minimal example subscriber.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1883", "broker TCP address")
	id := flag.String("id", "example-sub", "client id")
	filter := flag.String("filter", "demo/#", "topic filter")
	qos := flag.Int("qos", 1, "subscription QoS (0 or 1; 2 -> SUBACK 0x80)")
	duration := flag.Duration("for", 15*time.Second, "subscribe duration")
	clean := flag.Bool("clean", false, "clean session (false = durable)")
	flag.Parse()

	c, err := testsupport.Dial(*addr, "sub")
	must(err)
	ca, err := c.Connect(testsupport.ConnectOpts{
		ClientID: *id, CleanSession: *clean, KeepAlive: 30,
	})
	must(err)
	if ca.Code != packet.ConnAccepted {
		fmt.Fprintf(os.Stderr, "CONNECT rejected: 0x%02x\n", ca.Code)
		os.Exit(1)
	}
	fmt.Printf("connected; session_present=%v\n", ca.SessionPresent)

	must(c.SendSubscribe(1, *filter, byte(*qos)))
	pid, granted, err := c.ReadSuback(3 * time.Second)
	must(err)
	fmt.Printf("SUBACK id=%d granted=% x\n", pid, granted)

	deadline := time.Now().Add(*duration)
	for time.Now().Before(deadline) {
		f, err := c.ReadFrame(2 * time.Second)
		if err != nil {
			continue // idle, keep waiting until duration elapses
		}
		switch f.Type {
		case packet.TypePUBLISH:
			p, perr := f.ParsePublish()
			if perr != nil {
				fmt.Fprintln(os.Stderr, "bad publish:", perr)
				continue
			}
			fmt.Printf("RECV topic=%s qos=%d dup=%v retain=%v payload=%q\n",
				p.Topic, p.QoS, p.Dup, p.Retain, string(p.Payload))
			if p.QoS == 1 {
				must(c.SendPuback(p.PacketID))
			}
		case packet.TypePINGRESP:
			// ignore
		}
	}
	must(c.SendDisconnect())
	_ = c.ExpectClose(time.Second)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
