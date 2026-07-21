package blockchain

import (
	"fmt"
	"log"
	"net"
	"os"
)

func ShowError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func getForwardSlashPosition(value string) int {
	for i, c := range value {
		if c == '/' {
			return i
		}
	}

	return -1
}


// NODE_ADDRESS_ENV overrides automatic node address detection.
const NODE_ADDRESS_ENV = "DAANVEER_NODE_ADDRESS"

// GetNodeAddress returns the IP this node advertises to peers.
//
// Set NODE_ADDRESS_ENV to pin it explicitly -- required when running several
// nodes on one host, and for any reproducible multi-node experiment.
//
// Otherwise the first non-loopback IPv4 address of an interface that is up is
// used. The previous implementation returned the first address beginning with
// the literal string "192", which selected whichever 192.x interface the OS
// happened to list first (often the gateway) and panicked outright on any
// network not in 192.0.0.0/8 -- so it had to be hand-edited when moving
// between home and campus networks.
func GetNodeAddress() string {
	if pinned := os.Getenv(NODE_ADDRESS_ENV); pinned != "" {
		return pinned
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		log.Panic(err)
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addresses {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}

	fmt.Printf("no routable IPv4 interface found; falling back to 127.0.0.1. Set %s to override.\n", NODE_ADDRESS_ENV)
	return "127.0.0.1"
}