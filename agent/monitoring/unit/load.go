package monitoring

import (
	"github.com/shirou/gopsutil/v4/load"
	"math"
)

type LoadInfo struct {
	Load1  float64 `json:"load_1"`
	Load5  float64 `json:"load_5"`
	Load15 float64 `json:"load_15"`
	Valid  bool    `json:"-"`
}

func Load() LoadInfo {

	avg, err := load.Avg()
	if err != nil || avg == nil {
		return LoadInfo{Load1: 0, Load5: 0, Load15: 0}
	}
	for _, value := range []float64{avg.Load1, avg.Load5, avg.Load15} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return LoadInfo{}
		}
	}
	return LoadInfo{
		Load1:  avg.Load1,
		Load5:  avg.Load5,
		Load15: avg.Load15,
		Valid:  true,
	}

}
