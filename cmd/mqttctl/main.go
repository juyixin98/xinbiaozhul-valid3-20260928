// Command mqttctl is a tiny example client for exercising the local broker
// with the repository's own hand-rolled client. It demonstrates the protocol
// entry points used in the README; it is NOT a general-purpose client.
//
// Examples:
//
//	# subscribe (QoS1), durable session "reader":
//	mqttctl -addr 127.0.0.1:1883 -id reader -sub 'news/#' -qos 1 -clean=false
//
//	# publish a retained QoS1 message:
//	mqttctl -id writer -pub news/ping -msg hello -retain -qos 1
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"mqttd/internal/mqttclient"
	"mqttd/internal/packet"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1883", "broker address")
	id := flag.String("id", "mqttctl", "client id")
	clean := flag.Bool("clean", true, "clean session (false = persistent)")
	topicSub := flag.String("sub", "", "topic filter to subscribe to")
	topicPub := flag.String("pub", "", "topic to publish to")
	msg := flag.String("msg", "", "payload to publish")
	qos := flag.Int("qos", 0, "QoS for subscribe/publish (0 or 1)")
	retain := flag.Bool("retain", false, "publish with RETAIN=1")
	keepalive := flag.Uint("keepalive", 0, "keep-alive seconds")
	flag.Parse()

	if *qos < 0 || *qos > 1 {
		fmt.Fprintln(os.Stderr, "qos must be 0 or 1 (QoS 2 is unsupported by design)")
		os.Exit(2)
	}
	if *topicSub == "" && *topicPub == "" {
		fmt.Fprintln(os.Stderr, "specify -sub or -pub")
		os.Exit(2)
	}

	c, ev, err := mqttclient.Dial(*addr, mqttclient.Options{
		ClientID:     *id,
		CleanSession: *clean,
		KeepAlive:    uint16(*keepalive),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONNECT failed (return code %d): %v\n", ev.Conn.ReturnCode, err)
		os.Exit(1)
	}
	fmt.Printf("connected: session_present=%v return_code=%d\n",
		ev.Conn.SessionPresent, ev.Conn.ReturnCode)

	if *topicSub != "" {
		sa, err := c.Subscribe([]packet.SubFilter{{Topic: *topicSub, QoS: byte(*qos)}}, 3*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "SUBSCRIBE failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("subacked: pids=%d return_codes=% x\n", sa.PacketID, sa.ReturnCodes)
		for {
			ev, err := c.NextEvent(30 * time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "closed: %v\n", err)
				os.Exit(1)
			}
			if ev.Kind != mqttclient.EvPublish {
				continue
			}
			fmt.Printf("RECV topic=%s qos=%d dup=%v retain=%v pid=%d payload=%q\n",
				ev.Pub.Topic, ev.Pub.QoS, ev.Pub.Dup, ev.Pub.Retain, ev.Pub.PacketID, ev.Pub.Payload)
			if ev.Pub.QoS == 1 {
				if err := c.Puback(ev.Pub.PacketID); err != nil {
					fmt.Fprintf(os.Stderr, "puback: %v\n", err)
				}
			}
		}
	}

	if *topicPub != "" {
		switch *qos {
		case 0:
			if err := c.Publish0(*topicPub, []byte(*msg), *retain); err != nil {
				fmt.Fprintf(os.Stderr, "PUBLISH qos0: %v\n", err)
				os.Exit(1)
			}
		case 1:
			pid := c.AllocPID()
			if err := c.Publish1(pid, *topicPub, []byte(*msg), *retain, false, 3*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "PUBLISH qos1: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("pubacked pid=%d\n", pid)
		}
		if err := c.Disconnect(); err != nil {
			fmt.Fprintf(os.Stderr, "disconnect: %v\n", err)
			os.Exit(1)
		}
	}
}
