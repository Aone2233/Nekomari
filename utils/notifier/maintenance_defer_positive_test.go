package notifier

// 正面路径测试：维护窗口内离线 → 窗口结束后仍离线 → 必须补发。
//
// 仓库里原有的 maintenance_defer_test.go 只覆盖了清扫器的两个"丢弃"分支，而且是自己
// 手工设置 state.isConnExist（:163、:184），所以"抑制分支是否记录了离线事实"从来没被
// 验证过 —— 而死的正是这条路径：抑制分支提前 return，isConnExist 一直是 true，
// 清扫器于是把延迟通知当成"已恢复"丢掉。
//
// 这个用例不手工设置 isConnExist：连接状态由 updateOnlineState（真实上线路径）写入，
// 离线确认由 OfflineNotification 的真实抑制分支写入，投递由真实的 sweepDeferredAlerts
// 完成，中间经过真实的时间判断与 maintenance 规则，只把消息发送器换成测试内的记录器。

import (
	"sync"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	messageevent "github.com/Aone2233/nekomari/database/models/messageEvent"
	"github.com/Aone2233/nekomari/internal/config"
	"github.com/Aone2233/nekomari/utils/messageSender"
	"github.com/Aone2233/nekomari/utils/messageSender/factory"
)

// recordSender 实现 IEventMessageSender，把被真正投递出去的事件记下来。
// 用它替换真实 provider，用例才能断言"补发了"而不是"没报错"。
type recordSender struct {
	mu     sync.Mutex
	events []models.EventMessage
}

func (s *recordSender) GetName() string                         { return "notifier-test-recorder" }
func (s *recordSender) GetConfiguration() factory.Configuration { return &struct{}{} }
func (s *recordSender) Init() error                             { return nil }
func (s *recordSender) Destroy() error                          { return nil }
func (s *recordSender) SendTextMessage(message, title string) error {
	return nil
}

func (s *recordSender) SendEvent(event models.EventMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *recordSender) sent() []models.EventMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]models.EventMessage(nil), s.events...)
}

func (s *recordSender) reset() {
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
}

var testRecorder = &recordSender{}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender { return testRecorder })
}

// setupNotifierTestDB 建一个进程内的 SQLite 库（内存、不落盘），并把通知总开关打开。
// 只影响本包测试：本包其它用例不碰数据库。
func setupNotifierTestDB(t *testing.T) {
	t.Helper()

	oldType, oldFile := flags.DatabaseType, flags.DatabaseFile
	t.Cleanup(func() {
		flags.DatabaseType, flags.DatabaseFile = oldType, oldFile
	})

	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:notifier_deferred_positive?mode=memory&cache=shared"

	if err := dbcore.Initialize(); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	if err := config.Set(config.NotificationEnabledKey, true); err != nil {
		t.Fatalf("打开通知开关失败: %v", err)
	}
	if err := messageSender.LoadProvider("notifier-test-recorder", "{}"); err != nil {
		t.Fatalf("装载测试消息发送器失败: %v", err)
	}
	t.Cleanup(func() { testRecorder.reset() })
}

// deferredOfflineScenario 跑完整条链并断言补发确实发生：
// 窗口内离线（走真实的抑制分支）→ 窗口结束后仍离线 → 真实清扫器补发 1 条离线通知。
// 返回 clientID，供"节点随后重连"的用例接着走真实上线路径。
//
// clientID 由调用方传入：内存库在整个测试进程里是同一个，用例之间靠不同的 UUID 隔离。
func deferredOfflineScenario(t *testing.T, clientID string) string {
	t.Helper()
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate() // 窗口缓存是全局的，用例前后都清掉

	const connID = int64(4242)

	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{
		UUID: clientID, Token: "token-" + clientID, Name: "node",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("建客户端失败: %v", err)
	}
	if err := db.Create(&models.OfflineNotification{
		Client: clientID, Enable: true, GracePeriod: 1,
	}).Error; err != nil {
		t.Fatalf("建离线通知配置失败: %v", err)
	}

	windowStart := time.Now().UTC().Add(-time.Minute)
	windowEnd := time.Now().UTC().Add(400 * time.Millisecond)
	if err := db.Create(&models.MaintenanceWindow{
		Name:    "reboot",
		Start:   windowStart,
		End:     windowEnd,
		Clients: models.StringArray{clientID},
	}).Error; err != nil {
		t.Fatalf("建维护窗口失败: %v", err)
	}
	Invalidate() // 让新窗口立刻可见，而不是等 30 秒缓存过期

	// 先走真实的上线路径登记连接：isConnExist 由生产代码写成 true。
	if shouldNotify := updateOnlineState(clientID, connID); shouldNotify {
		t.Fatal("首次连接不应产生上线通知")
	}

	// 窗口内离线。抑制分支必须记录"离线事实成立"。
	OfflineNotification(clientID, connID)

	state := getOrInitState(clientID)
	state.mu.Lock()
	connExists := state.isConnExist
	stillPending := !state.pendingOfflineSince.IsZero()
	state.mu.Unlock()

	if queueLen() != 1 {
		t.Fatalf("抑制后的通知没有被排队（队列长度 %d）", queueLen())
	}
	if connExists {
		t.Fatal("抑制分支没有把 isConnExist 置 false：清扫器会据此认为节点已恢复，" +
			"延迟通知永远发不出去 —— 这正是本次修复的 bug")
	}
	if !stillPending {
		t.Fatal("pendingOfflineSince 应保持设置，否则重连会误发一条恢复通知")
	}

	// 窗口结束、节点仍然离线。等过窗口边界，再用真实时钟驱动清扫器。
	time.Sleep(time.Until(windowEnd) + 200*time.Millisecond)
	sweepDeferredAlerts(time.Now().UTC())

	sent := testRecorder.sent()
	if len(sent) != 1 {
		t.Fatalf("窗口结束后应补发 1 条离线通知，实际 %d 条", len(sent))
	}
	if sent[0].Event != messageevent.Offline {
		t.Fatalf("补发的事件类型 = %q，期望 %q", sent[0].Event, messageevent.Offline)
	}
	if len(sent[0].Clients) != 1 || sent[0].Clients[0].UUID != clientID {
		t.Fatalf("补发的事件指向的客户端不对: %+v", sent[0].Clients)
	}
	if queueLen() != 0 {
		t.Fatalf("补发之后队列应清空，实际长度 %d", queueLen())
	}

	var noti models.OfflineNotification
	if err := db.Where("client = ?", clientID).First(&noti).Error; err != nil {
		t.Fatalf("读回离线通知配置失败: %v", err)
	}
	if noti.LastNotified == nil {
		t.Fatal("补发成功后 last_notified 应被写入")
	}

	return clientID
}

// 正面路径本身：窗口内离线、窗口结束后仍离线 → 必须补发。
func TestDeferredAlertIsSentOnceTheWindowCloses(t *testing.T) {
	deferredOfflineScenario(t, "node-offline-in-window")
}

// 补发成功之后，这次离线就算"交代过了"：状态必须收敛（pendingOfflineSince 清零、
// isConnExist 保持 false），否则"窗口内离线 → 补发 → 节点重连"这条链里
// updateOnlineState 会看到 wasPending=true 直接 return false，恢复通知永远发不出去 ——
// 用户收到迟到的"掉了"，却永远等不到"回来了"。
func TestReconnectAfterADeferredAlertIsARecovery(t *testing.T) {
	clientID := deferredOfflineScenario(t, "node-recovery-update-online")

	// 补发后的状态必须与正常离线告警发出后（offline.go 的宽限期协程）一致。
	state := getOrInitState(clientID)
	state.mu.Lock()
	pendingCleared := state.pendingOfflineSince.IsZero()
	connExists := state.isConnExist
	state.mu.Unlock()

	if !pendingCleared {
		t.Fatal("补发成功后 pendingOfflineSince 应清零，否则重连会被当成「仍在待离线」而吞掉恢复通知")
	}
	if connExists {
		t.Fatal("补发成功后 isConnExist 应保持 false：离线事实仍然成立")
	}

	// 真实生产路径重连，不手工改任何标志。
	if shouldNotify := updateOnlineState(clientID, 4243); !shouldNotify {
		t.Fatal("补发过离线告警之后的重连应产生在线恢复通知，但 updateOnlineState 返回了 false")
	}
}

// 端到端那一半：重连不仅要被判成"恢复"，还要真的把在线通知投递出去。
func TestReconnectAfterADeferredAlertDeliversAnOnlineNotification(t *testing.T) {
	clientID := deferredOfflineScenario(t, "node-recovery-online-event")

	// 真实上线入口（内部会 forgetDeferredAlert + updateOnlineState + 发送）。
	OnlineNotification(clientID, 4243)

	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, event := range testRecorder.sent() {
			if event.Event == messageevent.Online {
				return // 收到恢复通知
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("重连后 3 秒内没有投递在线恢复通知；已记录的事件: %+v", testRecorder.sent())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
