package gpuops

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// xidRe matches NVIDIA kernel log lines such as "NVRM: Xid (PCI:0000:3b:00): 79".
var xidRe = regexp.MustCompile(`(?i)\bxid\b[^0-9]{0,24}(\d{1,4})`)

// fatalXIDs are treated as unrecoverable hardware faults worth cordon+drain.
// Meanings follow NVIDIA's Xid table; unknown codes stay warnings.
var fatalXIDs = map[int]string{
	48:  "Double-bit ECC error",
	63:  "ECC row remapping / page retirement failure",
	64:  "ECC page retirement",
	74:  "NVLink error",
	79:  "GPU has fallen off the bus",
	92:  "High single-bit ECC error rate",
	94:  "Contained ECC error",
	95:  "Uncontained ECC error",
	109: "Context switch timeout",
	119: "GSP RPC timeout",
	120: "GSP error",
	140: "Unrecovered ECC error",
}

// ParseXID returns the Xid code found in a message, or 0.
func ParseXID(message string) int {
	m := xidRe.FindStringSubmatch(message)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// ClassifyXID reports whether the code is treated as unrecoverable and a short reason.
func ClassifyXID(code int) (unrecoverable bool, reason string) {
	if reason, ok := fatalXIDs[code]; ok {
		return true, reason
	}
	if code > 0 {
		return false, fmt.Sprintf("Xid %d", code)
	}
	return false, ""
}

func isGPUText(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "xid") || strings.Contains(l, "ecc") ||
		strings.Contains(l, "nvlink") || strings.Contains(l, "gpu")
}
