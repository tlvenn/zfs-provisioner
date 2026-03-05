package client

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DetectGateway reads the default gateway from /proc/net/route.
// Returns the gateway IP as a string (e.g., "172.16.2.1").
func DetectGateway() (string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", fmt.Errorf("cannot read routing table: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// Default route has destination 00000000
		if fields[1] != "00000000" {
			continue
		}

		ip, err := parseHexIP(fields[2])
		if err != nil {
			return "", err
		}
		return ip, nil
	}

	return "", fmt.Errorf("no default gateway found")
}

// parseHexIP converts a hex-encoded little-endian IP from /proc/net/route to dotted notation.
func parseHexIP(hex string) (string, error) {
	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return "", fmt.Errorf("invalid gateway address %q: %w", hex, err)
	}
	return fmt.Sprintf("%d.%d.%d.%d",
		val&0xFF, (val>>8)&0xFF, (val>>16)&0xFF, (val>>24)&0xFF), nil
}
