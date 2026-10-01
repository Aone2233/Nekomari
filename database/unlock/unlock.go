// Package unlock 保存与读取节点的「流媒体 / AI 解锁」探测结果。
//
// 结果由节点上的探针产生并上报，服务端只负责存最近一次的快照。之所以必须由探针
// 来测：解锁取决于发起请求的那个 IP，服务端在别的机房，只能测到它自己。
package unlock

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Aone2233/nekomari/database/clients"
	"github.com/Aone2233/nekomari/database/dbcore"
	"github.com/Aone2233/nekomari/database/models"
	v2 "github.com/Aone2233/nekomari/protocol/v2"
	"gorm.io/gorm"
)

// ErrNotVisible 表示调用者无权读取该 uuid 的解锁快照：客户端表里没有这个 uuid，
// 或者它对应的节点是 hidden 而调用者不是管理员。
var ErrNotVisible = errors.New("unlock report is not readable for this caller")

// Save 覆盖写入一个节点的最近一次探测结果。
func Save(uuid string, params v2.UnlockParams) error {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		// 数据库还没起来。解锁是附加信息，不值得让上报流程炸掉。
		return errors.New("database is not initialized")
	}
	encoded, err := json.Marshal(params.Results)
	if err != nil {
		return err
	}
	probedAt := params.ProbedAt
	if probedAt.IsZero() {
		probedAt = time.Now().UTC()
	}
	report := models.UnlockReport{
		UUID:         uuid,
		EgressIP:     params.EgressIP,
		EgressRegion: params.EgressRegion,
		ProbedAt:     probedAt,
		Results:      string(encoded),
		UpdatedAt:    time.Now().UTC(),
	}
	// 一次探测就是一份完整快照，直接整体替换，不需要合并。
	return db.Save(&report).Error
}

// ClientHistoryReadable 判断某个 uuid 的节点数据是否可以返回给这个调用者。
//
// 规则与 web/rpc/jsonrpc 的 clientHistoryReadable 逐字相同，两个要点：
//
//   - **存在性是答案的一部分。** 客户端表里没有这个 uuid 时一律不可读，否则已删除
//     节点留存下来的数据会被当成「存在且可见」而返回；
//   - hidden 节点只对已登录管理员可读。
//
// 这里重新实现而不是共享同一个函数，是因为 jsonrpc 那份是包内私有的；公开的解锁
// 读取路径必须先过同一道门，两处必须同进同退。
func ClientHistoryReadable(hidden map[string]bool, loggedIn bool, uuid string) bool {
	isHidden, known := hidden[uuid]
	return known && (!isHidden || loggedIn)
}

// LoadVisible 读取 uuid 的解锁快照，并先做可见性判断。
//
// 这是公开读取路径唯一允许使用的入口：load 不再导出，调用者必须显式给出 loggedIn，
// 否则就会重现 P1-5 —— 匿名的 /api/public/ip-info/v1/lookup?uuid=… 按 uuid 拿到任意
// 节点探针上报的出口地址（egress_ip）。
//
// 返回 (nil, nil) 的两种情况是既有契约的一部分：uuid 为空（没有节点上下文，第三方主题
// 的按地址查询就是这么调用的），以及数据库未就绪（无库时也没有任何解锁数据可泄露，
// 而解锁只是附加信息，不该让整个 IP 信息接口失败）。
//
// uuid 存在但不可见时返回 ErrNotVisible，调用方必须把它渲染成与「不存在」完全相同的
// 错误 —— 两者可区分时，这个接口就成了隐藏节点存在性的判定器。
func LoadVisible(uuid string, loggedIn bool) (*v2.UnlockParams, error) {
	if uuid == "" {
		return nil, nil
	}
	// 用 ReadyDBInstance 而不是 GetDBInstance：后者会触发初始化，失败时直接结束进程，
	// 而 ip-info 的单元测试刻意不建库。
	if dbcore.ReadyDBInstance() == nil {
		return nil, nil
	}
	hidden, err := clients.HiddenClients()
	if err != nil {
		return nil, err
	}
	if !ClientHistoryReadable(hidden, loggedIn, uuid) {
		return nil, ErrNotVisible
	}
	return load(uuid)
}

// load 读取一个节点的探测结果，不做任何可见性判断。调用者必须先用 LoadVisible
// （或等价校验）确认调用者可以读这个 uuid。
//
// 没有记录时返回 (nil, nil) —— 「还没测过」不是错误，面板据此显示成「等待探测」
// 而不是报错。数据库不可用时同样返回 (nil, nil)：ip-info 的单元测试不建库，而解锁
// 只是这个接口的附加字段，不能因为它把地理与延迟数据一起拖垮。
func load(uuid string) (*v2.UnlockParams, error) {
	db := dbcore.ReadyDBInstance()
	if db == nil {
		return nil, nil
	}
	var report models.UnlockReport
	err := db.Where("uuid = ?", uuid).First(&report).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	params := v2.UnlockParams{
		EgressIP:     report.EgressIP,
		EgressRegion: report.EgressRegion,
		ProbedAt:     report.ProbedAt,
	}
	if report.Results != "" {
		if err := json.Unmarshal([]byte(report.Results), &params.Results); err != nil {
			// 存进去的 JSON 解不出来，说明记录已经损坏。返回空结果而不是让整个
			// ip-info 接口失败 —— 解锁面板是附加信息，不该拖垮地理与延迟数据。
			params.Results = nil
		}
	}
	return &params, nil
}
