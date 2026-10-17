package monitoring

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

func parseTCPCounters(reader io.Reader) (out, retrans uint64, ok bool) {
	scanner := bufio.NewScanner(reader)
	var keys []string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "Tcp:" {
			continue
		}
		if keys == nil {
			keys = fields
			continue
		}
		if len(keys) != len(fields) {
			return 0, 0, false
		}
		found := 0
		for i, key := range keys {
			if key != "OutSegs" && key != "RetransSegs" {
				continue
			}
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return 0, 0, false
			}
			if key == "OutSegs" {
				out = value
			} else {
				retrans = value
			}
			found++
		}
		return out, retrans, found == 2 && scanner.Err() == nil
	}
	return 0, 0, false
}

type tcpCounterSampler struct {
	mu           sync.Mutex
	out, retrans uint64
	ready        bool
}

func (s *tcpCounterSampler) sample(out, retrans uint64) *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldOut, oldRetrans, ready := s.out, s.retrans, s.ready
	s.out, s.retrans, s.ready = out, retrans, true
	if !ready || out <= oldOut || retrans < oldRetrans {
		return nil
	}
	value := float64(retrans-oldRetrans) / float64(out-oldOut) * 100
	if value > 100 {
		return nil
	}
	return &value
}

var retransSampler tcpCounterSampler

func tcpRetransPercent() *float64 {
	root := flags.HostProc
	if root == "" {
		root = "/proc"
	}
	file, err := os.Open(filepath.Join(root, "net", "snmp"))
	if err != nil {
		return nil
	}
	defer file.Close()
	out, retrans, ok := parseTCPCounters(file)
	if !ok {
		return nil
	}
	return retransSampler.sample(out, retrans)
}
