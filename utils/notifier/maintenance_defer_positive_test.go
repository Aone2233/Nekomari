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
	"fmt"
	"sync"
	"sync/atomic"
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
//
// onSend 让用例在"一条通知正在发送"的那一瞬间插进一段代码，用来复现判定与发送之间
// 的竞态（节点在这一瞬重连、或再次离线并排队）。它在事件被记录之后、锁之外调用。
type recordSender struct {
	mu     sync.Mutex
	events []models.EventMessage
	onSend func(models.EventMessage)
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
	s.events = append(s.events, event)
	hook := s.onSend
	s.mu.Unlock()
	if hook != nil {
		hook(event)
	}
	return nil
}

func (s *recordSender) sent() []models.EventMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]models.EventMessage(nil), s.events...)
}

func (s *recordSender) sawEvent(kind string) bool {
	for _, event := range s.sent() {
		if event.Event == kind {
			return true
		}
	}
	return false
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

// notifierTestSeq 让同一个测试进程里的每次运行都拿到不同的节点 UUID。
//
// 测试库是进程内的内存库，而 dbcore.Initialize 只会真正执行一次，所以 `-count=2` 会复用
// 同一个库和库里的数据：固定的 UUID / token 会让第二轮直接
// `UNIQUE constraint failed: clients.token` 失败。计数器就是可重复性的来源。
var notifierTestSeq atomic.Int64

func uniqueNode(base string) string {
	return fmt.Sprintf("%s-%d", base, notifierTestSeq.Add(1))
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

// notifierTestNode 建一个客户端和它的离线通知配置，返回唯一且可重复的 clientID。
func notifierTestNode(t *testing.T, base string) string {
	t.Helper()

	clientID := uniqueNode(base)
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
	return clientID
}

// notifierTestWindow 建一个覆盖给定节点的维护窗口。
func notifierTestWindow(t *testing.T, name string, start, end time.Time, clientIDs ...string) {
	t.Helper()

	if err := dbcore.GetDBInstance().Create(&models.MaintenanceWindow{
		Name:    name,
		Start:   start,
		End:     end,
		Clients: models.StringArray(clientIDs),
	}).Error; err != nil {
		t.Fatalf("建维护窗口 %s 失败: %v", name, err)
	}
}

// deferredOfflineScenario 跑完整条链并断言补发确实发生：
// 窗口内离线（走真实的抑制分支）→ 窗口结束后仍离线 → 真实清扫器补发 1 条离线通知。
// 返回 clientID，供"节点随后重连"的用例接着走真实上线路径。
func deferredOfflineScenario(t *testing.T, base string) string {
	t.Helper()
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate() // 窗口缓存是全局的，用例前后都清掉

	const connID = int64(4242)

	clientID := notifierTestNode(t, base)

	windowStart := time.Now().UTC().Add(-time.Minute)
	windowEnd := time.Now().UTC().Add(400 * time.Millisecond)
	notifierTestWindow(t, "reboot", windowStart, windowEnd, clientID)
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

	db := dbcore.GetDBInstance()
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

// 窗口 A 结束前又进了窗口 B：告警必须改排到 B 结束，而不是被清扫器删掉。
//
// 这是 A7-2 的回归用例。旧代码里 sendDeferredOfflineAlert 看到窗口 B 仍覆盖节点，于是
// 重新入队并 return nil；清扫器紧接着无条件 forgetDeferredAlert，把刚写进去的那条删了，
// 于是"在 A 内离线、B 结束前一直没恢复"的节点永久丢掉了这条通知。
//
// 时钟是受控的：窗口用远超用例耗时的长度建立，清扫则用显式传入的 now 驱动，
// 所以既不需要 sleep，也不会因为机器慢而踩到窗口边界。
func TestDeferredAlertSurvivesASecondWindowTakingOver(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()

	clientID := notifierTestNode(t, "node-second-window")

	now := time.Now().UTC()
	// A 先结束（Evaluate 取最近结束的窗口，所以抑制这条通知的是 A），B 比它晚得多。
	windowAEnd := now.Add(30 * time.Second)
	windowBEnd := now.Add(4 * time.Minute)
	notifierTestWindow(t, "window-a", now.Add(-time.Minute), windowAEnd, clientID)
	notifierTestWindow(t, "window-b", now.Add(-time.Minute), windowBEnd, clientID)
	Invalidate()

	if shouldNotify := updateOnlineState(clientID, 4242); shouldNotify {
		t.Fatal("首次连接不应产生上线通知")
	}
	OfflineNotification(clientID, 4242)

	if queueLen() != 1 {
		t.Fatalf("窗口内离线没有被排队（队列长度 %d）", queueLen())
	}
	deferredMu.Lock()
	queued := deferredAlerts[clientID]
	deferredMu.Unlock()
	if !queued.deliverAt.Equal(windowAEnd.UTC()) {
		t.Fatalf("抑制它的应是先结束的窗口 A（deliverAt = %s，期望 %s）", queued.deliverAt, windowAEnd.UTC())
	}

	// A 结束、B 仍在：清扫器必须把它改排到 B 结束。
	sweepDeferredAlerts(windowAEnd.Add(time.Second))

	if queueLen() != 1 {
		t.Fatalf("窗口 B 接管之后告警被删掉了（队列长度 %d）：节点在 A 内离线、B 结束前一直没恢复，"+
			"通知于是永久丢失", queueLen())
	}
	deferredMu.Lock()
	queued = deferredAlerts[clientID]
	deferredMu.Unlock()
	if !queued.deliverAt.Equal(windowBEnd.UTC()) {
		t.Fatalf("改排后的 deliverAt = %s，期望窗口 B 的结束 %s", queued.deliverAt, windowBEnd.UTC())
	}
	if len(testRecorder.sent()) != 0 {
		t.Fatalf("窗口 B 还没结束就发了通知: %+v", testRecorder.sent())
	}

	// B 结束、节点仍然离线：这时候才补发。
	sweepDeferredAlerts(windowBEnd.Add(time.Second))

	sent := testRecorder.sent()
	if len(sent) != 1 {
		t.Fatalf("窗口 B 结束后应补发 1 条离线通知，实际 %d 条", len(sent))
	}
	if sent[0].Event != messageevent.Offline {
		t.Fatalf("补发的事件类型 = %q，期望 %q", sent[0].Event, messageevent.Offline)
	}
	if queueLen() != 0 {
		t.Fatalf("补发之后队列应清空，实际长度 %d", queueLen())
	}
}

// A7-3 的回归用例：stillOffline 判定与真正发送之间，节点重连。
//
// 判定本身没法原子化，代码能保证的是"判定"与"标记这次离线已上报"落在同一个临界区里，
// 也就是在发送之前认领。旧代码把清零放在发送之后：发送途中的重连看到 wasPending=true，
// updateOnlineState 于是返回 false —— 操作员收到一条迟到的"掉了"，而"回来了"永远不发。
//
// 重连用真实生产入口 OnlineNotification，不手工改任何标志。
func TestReconnectDuringTheSendStillProducesARecovery(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()

	clientID := notifierTestNode(t, "node-reconnect-during-send")

	now := time.Now().UTC()
	windowEnd := now.Add(30 * time.Second)
	notifierTestWindow(t, "reboot", now.Add(-time.Minute), windowEnd, clientID)
	Invalidate()

	if shouldNotify := updateOnlineState(clientID, 4242); shouldNotify {
		t.Fatal("首次连接不应产生上线通知")
	}
	OfflineNotification(clientID, 4242)
	if queueLen() != 1 {
		t.Fatalf("窗口内离线没有被排队（队列长度 %d）", queueLen())
	}

	// 告警正在发送的那一瞬间，节点连了上来。
	var once sync.Once
	testRecorder.onSend = func(event models.EventMessage) {
		if event.Event != messageevent.Offline {
			return
		}
		once.Do(func() { OnlineNotification(clientID, 4243) })
	}
	t.Cleanup(func() { testRecorder.onSend = nil })

	sweepDeferredAlerts(windowEnd.Add(time.Second))

	// 那次离线是真的，所以这条迟到的离线告警应该发出去……
	if !testRecorder.sawEvent(messageevent.Offline) {
		t.Fatalf("离线告警没有发出；已记录的事件: %+v", testRecorder.sent())
	}
	// ……而且随后的重连必须被当成"恢复"并真的投递，而不是被 wasPending 吞掉。
	deadline := time.Now().Add(3 * time.Second)
	for !testRecorder.sawEvent(messageevent.Online) {
		if time.Now().After(deadline) {
			t.Fatalf("发送过程中重连之后没有投递在线恢复通知（重连被当成「仍在待离线」而吞掉）；"+
				"已记录的事件: %+v", testRecorder.sent())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 发送途中节点在新的连接（999）下再次离线并排队：那条新条目不能被这次发送的清理顺手删掉。
//
// 同一个 client id 下只有一条队列项，靠 client id 删就会把新条目当成刚投递的那条删掉 ——
// 与 A7-2 同一种形状的 bug，只是低一层。
func TestANewerQueuedAlertSurvivesTheOlderOnesCleanup(t *testing.T) {
	clearDeferred(t)
	setupNotifierTestDB(t)
	Invalidate()

	clientID := notifierTestNode(t, "node-newer-queue-entry")

	now := time.Now().UTC()
	windowEnd := now.Add(30 * time.Second)
	notifierTestWindow(t, "reboot", now.Add(-time.Minute), windowEnd, clientID)
	Invalidate()

	if shouldNotify := updateOnlineState(clientID, 4242); shouldNotify {
		t.Fatal("首次连接不应产生上线通知")
	}
	OfflineNotification(clientID, 4242)

	newerEnd := windowEnd.Add(10 * time.Minute)
	var once sync.Once
	testRecorder.onSend = func(event models.EventMessage) {
		if event.Event != messageevent.Offline {
			return
		}
		once.Do(func() { deferOfflineAlert(clientID, newerEnd, 999) })
	}
	t.Cleanup(func() { testRecorder.onSend = nil })

	sweepDeferredAlerts(windowEnd.Add(time.Second))

	if queueLen() != 1 {
		t.Fatalf("新排队的条目被旧告警的清理删掉了（队列长度 %d）", queueLen())
	}
	deferredMu.Lock()
	queued := deferredAlerts[clientID]
	deferredMu.Unlock()
	if queued.connectionID != 999 || !queued.deliverAt.Equal(newerEnd.UTC()) {
		t.Fatalf("留下的条目 = %+v，期望新连接 999 在 %s 的排队", queued, newerEnd.UTC())
	}
}
