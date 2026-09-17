// Minimal raw-ICMP socket probe, built and run with the same file capability as
// the agent. Distinguishes "the capability does not work here" from "the agent
// does something else".
package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	fmt.Println("uid:", os.Getuid())

	// Exactly what pro-bing does when SetPrivileged(true).
	c, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		fmt.Println("ip4:icmp (raw):", err)
	} else {
		fmt.Println("ip4:icmp (raw): OK")
		c.Close()
	}

	// Unprivileged ping socket path (needs net.ipv4.ping_group_range).
	c2, err := net.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		fmt.Println("udp4:", err)
	} else {
		c2.Close()
	}
}
