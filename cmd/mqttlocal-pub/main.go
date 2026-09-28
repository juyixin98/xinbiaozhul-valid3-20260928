// Command mqttlocal-pub is a minimal example publisher using only the
// project's own codec over a TCP socket — no third-party MQTT library.
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
	id := flag.String("id", "example-pub", "client id")
	topic := flag.String("topic", "demo/hello", "topic name")
	msg := flag.String("msg", "hello", "message payload")
	qos := flag.Int("qos", 0, "QoS level (0 or 1)")
	retain := flag.Bool("retain", false, "set RETAIN flag")
	clean := flag.Bool("clean", true, "clean session")
	flag.Parse()

	if *qos < 0 || *qos > 1 {
		fmt.Fprintln(os.Stderr, "only QoS 0/1 are supported")
		os.Exit(2)
	}

	c, err := testsupport.Dial(*addr, "pub")
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

	var pubID uint16
	if *qos == 1 {
		pubID = 1
	}
	must(c.SendPublish(*topic, []byte(*msg), byte(*qos), pubID, false, *retain))
	if *qos == 1 {
		f, err := c.ReadFrame(3 * time.Second)
		must(err)
		if f.Type != packet.TypePUBACK {
			fmt.Fprintf(os.Stderr, "expected PUBACK, got %s\n", f.Type)
			os.Exit(1)
		}
		fmt.Printf("PUBACK received (publisher packet id echoed)\n")
	}
	fmt.Printf("published topic=%s qos=%d retain=%v bytes=%d\n",
		*topic, *qos, *retain, len(*msg))
	must(c.SendDisconnect())
	_ = c.ExpectClose(time.Second)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
