package server

import (
	"context"
	"log"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
	"github.com/Aone2233/nekomari/agent/routeprobe"
	"github.com/Aone2233/nekomari/agent/unlock"
)

// unlockProbeInterval 是两次解锁探测之间的间隔。
//
// 解锁状态跟着出口地区走，几小时才可能变一次，所以没有理由更频繁：每次探测都会真的
// 去打 Netflix、YouTube 和 Cloudflare，太频繁既不礼貌也拿不到新信息。
const unlockProbeInterval = 6 * time.Hour

// unlockProbeBudget 是单轮探测的整体预算，超时就只上报已经拿到的结论。
const unlockProbeBudget = 2 * time.Minute

// DoUploadUnlockWorks 定期探测并上报流媒体/AI 解锁状态。
//
// 必须在节点上跑，不能在服务端跑：解锁取决于发起请求的那个 IP。服务端在别的机房，
// 只能测到它自己的出口。
func DoUploadUnlockWorks() {
	targets, err := routeprobe.ParseTargets(flags.RouteTargets)
	if err != nil {
		log.Println("Route probe configuration invalid:", err)
	}
	var report unlock.Report
	var state routeprobe.State
	var nextMedia, nextRoute time.Time
	pending := false
	for {
		now := time.Now().UTC()
		if !now.Before(nextMedia) {
			ctx, cancel := context.WithTimeout(context.Background(), unlockProbeBudget)
			report = unlock.Probe(ctx, unlock.NewClient())
			cancel()
			nextMedia = now.Add(unlockProbeInterval)
			pending = true
		}
		// Independent clocks: a six-hour media refresh must neither delay a daily
		// route probe nor make yesterday's route evidence appear newly measured.
		if len(targets) > 0 && !time.Now().Before(nextRoute) {
			ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
			cached, stateErr := routeprobe.CachedProbe(ctx, flags.RouteStateFile, targets, time.Now().UTC(), routeprobe.Probe)
			cancel()
			if stateErr != nil {
				log.Println("Route probe state:", stateErr)
				nextRoute = time.Now().Add(time.Minute)
			} else {
				nextRoute = cached.ProbedAt.Add(routeprobe.Interval)
			}
			if !cached.ProbedAt.IsZero() {
				pending = pending || !state.ProbedAt.Equal(cached.ProbedAt)
				state = cached
			}
		}
		report.Routes, report.RoutesProbedAt = state.Results, state.ProbedAt
		if pending {
			if err := postV2RPC(v2.BuildUnlockPayload(report)); err != nil {
				log.Println("Error uploading unlock report:", err)
			} else {
				log.Printf("Unlock probe: region=%s results=%d routes=%d", report.EgressRegion, len(report.Results), len(report.Routes))
				pending = false
			}
		}
		now = time.Now()
		delay := nextProbeDelay(now, nextMedia, nextRoute, len(targets) > 0)
		if pending && delay > time.Minute {
			delay = time.Minute
		}
		time.Sleep(delay)
	}
}

func nextProbeDelay(now, media, route time.Time, hasRoutes bool) time.Duration {
	due := media
	if hasRoutes && route.Before(due) {
		due = route
	}
	if delay := due.Sub(now); delay > time.Second {
		return delay
	}
	return time.Second
}
