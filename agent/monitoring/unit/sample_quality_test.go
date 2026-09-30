package monitoring

import (
	"math"
	"testing"
	"time"
)

func TestNetworkEpochChangesOnlyAcrossContinuityBoundaries(t *testing.T) {
	networkSpeedSample.Lock()
	upBefore, downBefore := networkSpeedSample.totalUp, networkSpeedSample.totalDown
	atBefore, scopeBefore := networkSpeedSample.sampledAt, networkSpeedSample.scope
	epochBefore, validBefore := networkSpeedSample.epoch, networkSpeedSample.rateValid
	generationBefore := networkSpeedSample.generation
	networkSpeedSample.sampledAt = time.Time{}
	networkSpeedSample.Unlock()
	t.Cleanup(func() {
		networkSpeedSample.Lock()
		defer networkSpeedSample.Unlock()
		networkSpeedSample.totalUp, networkSpeedSample.totalDown = upBefore, downBefore
		networkSpeedSample.sampledAt, networkSpeedSample.scope = atBefore, scopeBefore
		networkSpeedSample.epoch, networkSpeedSample.rateValid = epochBefore, validBefore
		networkSpeedSample.generation = generationBefore
	})
	base := time.Now().UTC()
	up, down := updateNetworkSpeedSampleWithScope(1000, 2000, base, "eth0")
	_, first, valid := NetworkSampleMetadata()
	if up != 0 || down != 0 || valid || first == "" {
		t.Fatal("first sample invented a rate")
	}
	up, down = updateNetworkSpeedSampleWithScope(1050, 2100, base.Add(5*time.Second), "eth0")
	_, second, valid := NetworkSampleMetadata()
	if up != 10 || down != 20 || !valid || first != second {
		t.Fatal("continuous sample changed epoch or rates")
	}
	up, down = updateNetworkSpeedSampleWithScope(10, 20, base.Add(10*time.Second), "eth0")
	_, reset, valid := NetworkSampleMetadata()
	if up != 0 || down != 0 || valid || reset == second {
		t.Fatal("counter reset must change epoch and invalidate rate")
	}
	updateNetworkSpeedSampleWithScope(1000, 2000, base.Add(15*time.Second), "eth1")
	_, scope, valid := NetworkSampleMetadata()
	if scope == reset || valid {
		t.Fatal("NIC scope change must invalidate rate")
	}
}

func TestMemoryQualityRejectsMissingAndImpossibleFields(t *testing.T) {
	info := &ProcMemInfo{MemTotal: 1000, MemFree: 100, Cached: 200, Buffers: 50, SReclaimable: 25, Shmem: 10, Seen: map[string]bool{}}
	if memHtopLikeFrom(info).Valid {
		t.Fatal("missing fields became zero measurements")
	}
	for _, key := range []string{"MemTotal", "MemFree", "Cached", "Buffers", "SReclaimable", "Shmem"} {
		info.Seen[key] = true
	}
	if got := memHtopLikeFrom(info); !got.Valid || got.Used != 635 {
		t.Fatalf("htop definition: %+v", got)
	}
	info.MemFree = 1001
	if memHtopLikeFrom(info).Valid {
		t.Fatal("free exceeds total")
	}
	info.MemFree, info.Cached = 100, math.MaxUint64
	if memHtopLikeFrom(info).Valid {
		t.Fatal("overflow accepted")
	}
}
