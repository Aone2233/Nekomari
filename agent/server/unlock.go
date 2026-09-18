package server

import (
	"context"
	"log"
	"time"

	v2 "github.com/Aone2233/nekomari/agent/protocol/v2"
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
	// 先探一次，面板不必等满一个周期。
	if err := uploadUnlock(); err != nil {
		log.Println("Error uploading unlock report:", err)
	}

	ticker := time.NewTicker(unlockProbeInterval)
	for range ticker.C {
		if err := uploadUnlock(); err != nil {
			log.Println("Error uploading unlock report:", err)
		}
	}
}

func uploadUnlock() error {
	ctx, cancel := context.WithTimeout(context.Background(), unlockProbeBudget)
	defer cancel()

	report := unlock.Probe(ctx, unlock.NewClient())
	log.Printf("Unlock probe: egress=%s/%s results=%d",
		report.EgressIP, report.EgressRegion, len(report.Results))
	return postV2RPC(v2.BuildUnlockPayload(report))
}
