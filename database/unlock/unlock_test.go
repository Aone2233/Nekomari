package unlock

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aone2233/nekomari/cmd/flags"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	"github.com/Aone2233/nekomari/internal/dbcache"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
)

// 解锁快照的可读性判断要查客户端表，所以这个包必须建库；ip-info 的单元测试则刻意不建库
// （database/unlock.Load / LoadVisible 在没有库时返回空而不是失败）。两边都是刻意的。
const (
	visibleUUID  = "unlock-visible"
	hiddenUUID   = "unlock-hidden"
	noReportUUID = "unlock-noreport"
	unknownUUID  = "unlock-never-existed"

	visibleEgress = "203.0.113.9"
	hiddenEgress  = "203.0.113.10"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:database_unlock_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}

	fixtures := []models.Client{
		{UUID: visibleUUID, Name: "visible", Token: "test-only-token-unlock-visible"},
		{UUID: hiddenUUID, Name: "hidden", Token: "test-only-token-unlock-hidden", Hidden: true},
		{UUID: noReportUUID, Name: "no report yet", Token: "test-only-token-unlock-noreport"},
	}
	for _, client := range fixtures {
		if err := db.Create(&client).Error; err != nil {
			fmt.Fprintf(os.Stderr, "seed client %s: %v\n", client.UUID, err)
			os.Exit(1)
		}
	}
	if err := Save(visibleUUID, probeReport(visibleEgress)); err != nil {
		fmt.Fprintf(os.Stderr, "seed visible report: %v\n", err)
		os.Exit(1)
	}
	if err := Save(hiddenUUID, probeReport(hiddenEgress)); err != nil {
		fmt.Fprintf(os.Stderr, "seed hidden report: %v\n", err)
		os.Exit(1)
	}
	dbcache.InvalidateAll()

	code := m.Run()
	_ = dbcore.Close()
	os.Exit(code)
}

func probeReport(egress string) v2.UnlockParams {
	return v2.UnlockParams{
		EgressIP:     egress,
		EgressRegion: "JP",
		ProbedAt:     time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Results: []v2.UnlockItem{
			{ID: "netflix", Name: "Netflix", Kind: "media", Status: "ok", Basis: "probe"},
		},
	}
}

// TestClientHistoryReadableMatchesTheJsonrpcRule 把规则本身钉死，包括最容易写错的
// 一条：**存在性是答案的一部分**。客户端表里没有的 uuid 一律不可读，否则已删除节点
// 留存下来的解锁数据会被当成「存在且可见」返回。
func TestClientHistoryReadableMatchesTheJsonrpcRule(t *testing.T) {
	hidden := map[string]bool{visibleUUID: false, hiddenUUID: true}
	cases := []struct {
		name     string
		loggedIn bool
		uuid     string
		want     bool
	}{
		{"visible node, guest", false, visibleUUID, true},
		{"visible node, admin", true, visibleUUID, true},
		{"hidden node, guest", false, hiddenUUID, false},
		{"hidden node, admin", true, hiddenUUID, true},
		{"deleted node, guest", false, unknownUUID, false},
		{"deleted node, admin", true, unknownUUID, false},
		{"empty uuid", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientHistoryReadable(hidden, tc.loggedIn, tc.uuid); got != tc.want {
				t.Fatalf("ClientHistoryReadable(%q, loggedIn=%v) = %v, want %v",
					tc.uuid, tc.loggedIn, got, tc.want)
			}
		})
	}
}

// TestLoadVisibleGuestReadsOnlyNonHiddenNodes 是 P1-5 的核心：匿名的公开读取路径
// 只能拿到「存在 && 非 hidden」节点的解锁快照 —— 快照里带着探针的实际出口地址。
func TestLoadVisibleGuestReadsOnlyNonHiddenNodes(t *testing.T) {
	report, err := LoadVisible(visibleUUID, false)
	if err != nil {
		t.Fatalf("visible node: unexpected error: %v", err)
	}
	if report == nil || report.EgressIP != visibleEgress || len(report.Results) == 0 {
		t.Fatalf("visible node report = %+v, want the stored snapshot with %s", report, visibleEgress)
	}

	for _, uuid := range []string{hiddenUUID, unknownUUID} {
		report, err := LoadVisible(uuid, false)
		if !errors.Is(err, ErrNotVisible) {
			t.Fatalf("LoadVisible(%q, guest) error = %v, want ErrNotVisible", uuid, err)
		}
		if report != nil {
			t.Fatalf("LoadVisible(%q, guest) returned a snapshot: %+v", uuid, report)
		}
	}

	// 「还没探测过」不是错误：可见节点没有记录时返回空。
	report, err = LoadVisible(noReportUUID, false)
	if err != nil || report != nil {
		t.Fatalf("node without a probe = (%+v, %v), want (nil, nil)", report, err)
	}

	// 空 uuid 是没有节点上下文，公开面板的按地址查询就是这样调用的。
	report, err = LoadVisible("", false)
	if err != nil || report != nil {
		t.Fatalf("empty uuid = (%+v, %v), want (nil, nil)", report, err)
	}
}

// TestLoadVisibleAdminReadsHiddenNodes 是另一半：hidden 节点对已登录管理员仍然可读，
// 与 public:* 读取历史的约定一致。注意已删除的 uuid 对管理员同样不可读。
func TestLoadVisibleAdminReadsHiddenNodes(t *testing.T) {
	report, err := LoadVisible(hiddenUUID, true)
	if err != nil {
		t.Fatalf("admin + hidden node: unexpected error: %v", err)
	}
	if report == nil || report.EgressIP != hiddenEgress {
		t.Fatalf("admin + hidden node report = %+v, want the stored snapshot with %s", report, hiddenEgress)
	}

	if _, err := LoadVisible(unknownUUID, true); !errors.Is(err, ErrNotVisible) {
		t.Fatalf("admin + deleted node error = %v, want ErrNotVisible (existence is required)", err)
	}
}

// TestSaveRoundTripsThroughLoadVisible 钉住写入与读取的字段（尤其是 Results 的 JSON）。
func TestSaveRoundTripsThroughLoadVisible(t *testing.T) {
	const uuid = "unlock-roundtrip"
	db := dbcore.GetDBInstance()
	if err := db.Create(&models.Client{UUID: uuid, Name: "roundtrip", Token: "test-only-token-unlock-roundtrip"}).Error; err != nil {
		t.Fatal(err)
	}
	dbcache.InvalidateAll()

	want := v2.UnlockParams{
		EgressIP:     "198.51.100.7",
		EgressRegion: "HK",
		ProbedAt:     time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC),
		Results: []v2.UnlockItem{
			{ID: "chatgpt", Name: "ChatGPT", Kind: "ai", Status: "blocked", Basis: "probe", Detail: "captcha"},
		},
	}
	if err := Save(uuid, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadVisible(uuid, false)
	if err != nil {
		t.Fatalf("load visible: %v", err)
	}
	if got == nil {
		t.Fatal("saved report is not readable")
	}
	if got.EgressIP != want.EgressIP || got.EgressRegion != want.EgressRegion ||
		!got.ProbedAt.Equal(want.ProbedAt) || len(got.Results) != 1 || got.Results[0] != want.Results[0] {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
