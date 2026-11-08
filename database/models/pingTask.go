package models

import "time"

type PingRecord struct {
	Client     string    `json:"client" gorm:"type:varchar(36);not null;index"`
	ClientInfo Client    `json:"client_info" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	TaskId     uint      `json:"task_id" gorm:"not null;index"`
	Task       PingTask  `json:"task" gorm:"foreignKey:TaskId;references:Id;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;"`
	Time       time.Time `json:"time" gorm:"index;not null"`
	Value      int       `json:"value" gorm:"type:int;not null"` // Ping 值，单位毫秒
	// ValueUS 是同一测量结果的微秒精度值（新 agent 上报的 value_us）。
	//
	// 它不是数据库列（gorm:"-"）：落库形态仍然是 ping.latency_ms 那一条序列，只是值
	// 从整数毫秒换成小数毫秒。这个字段只负责把精度从上报入口带到 metric store，并在
	// 读回时把小数毫秒还原成微秒交给前端。0 表示「旧 agent 没有上报」，JSON 里省略。
	ValueUS int64 `json:"value_us,omitempty" gorm:"-"`
	// PingType 记录本条结果是用哪种协议测出来的（icmp/tcp/http）。
	// 双协议并列探测（dual）会让同一个任务在同一周期产出多条结果，
	// 靠这个字段区分，避免两条序列互相覆盖。
	PingType string `json:"ping_type" gorm:"type:varchar(12);not null;default:'';index"`
	// Role 区分本条结果测的是主目标（空/""）还是参考点（"reference"）。
	Role string `json:"role" gorm:"type:varchar(12);not null;default:'';index"`
	// Family 记录本条结果【实际使用】的地址族（"ipv4"/"ipv6"）。
	// 目标是域名时由各节点自行解析，双栈域名在不同节点上可能落到不同族 ——
	// 那种情况下同一任务的两条曲线描述的是两条不同的路径，不能当作可比数据。
	// 空值表示未上报（旧 agent）或无法判断，此时保持改动前的行为。
	Family string `json:"family" gorm:"type:varchar(8);not null;default:'';index"`
	// Additive v1.8 multi-probe summary. These are not database columns; the
	// metric store turns them into ping.attempts/success/loss_pct/min/avg/max/
	// jitter points. Attempts == 0 means a legacy single-probe record.
	Attempts int                  `json:"attempts,omitempty" gorm:"-"`
	Success  int                  `json:"success,omitempty" gorm:"-"`
	LossPct  float64              `json:"loss_pct,omitempty" gorm:"-"`
	MinUS    int64                `json:"min_us,omitempty" gorm:"-"`
	AvgUS    int64                `json:"avg_us,omitempty" gorm:"-"`
	MaxUS    int64                `json:"max_us,omitempty" gorm:"-"`
	JitterUS int64                `json:"jitter_us,omitempty" gorm:"-"`
	HTTP     *PingHTTPObservation `json:"http,omitempty" gorm:"-"`
}

// PingHTTPObservation is the additive HTTP phase breakdown carried from the
// wire into the metric writer. Pointers keep "not measured" distinct from zero.
type PingHTTPObservation struct {
	DNSUS         *int64   `json:"dns_us,omitempty"`
	TCPUS         *int64   `json:"tcp_us,omitempty"`
	TLSUS         *int64   `json:"tls_us,omitempty"`
	TTFBUS        *int64   `json:"ttfb_us,omitempty"`
	TotalUS       *int64   `json:"total_us,omitempty"`
	StatusCode    *int     `json:"status_code,omitempty"`
	Redirects     *int     `json:"redirects,omitempty"`
	TLSExpiryDays *float64 `json:"tls_expiry_days,omitempty"`
	Reachable     *bool    `json:"reachable,omitempty"`
	Available     *bool    `json:"available,omitempty"`
}

// PingTask 表示一次延迟监测任务配置。
type PingTask struct {
	Id        uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	ConfigUID string      `json:"-" gorm:"type:varchar(36);not null;default:''"`
	Weight    int         `json:"weight" gorm:"type:int;not null;default:0;index"`
	Name      string      `json:"name" gorm:"type:varchar(255);not null;index"`
	Clients   StringArray `json:"clients" gorm:"type:longtext"`
	DefaultOn bool        `json:"default_on" gorm:"column:all_clients;not null;default:false"` // 新加入的服务器是否自动开启此监测；现有服务器不受此字段影响
	Type      string      `json:"type" gorm:"type:varchar(12);not null;default:'icmp'"`        // icmp tcp http auto dual
	Target    string      `json:"target" gorm:"type:varchar(255);not null"`                    // Ping 目标地址
	// Reference 是可选【参考目标】，用于路径归因：同一个探针在同一个周期里
	// 既测目标也测参考点（典型做法是填本机网关或一个已知良好的节点）。
	// 两者并列后就能区分「本机/内网出问题」还是「上游线路/代理出问题」，
	// 而不是只看到一条延迟曲线在抖却不知道是哪一段。
	Reference string `json:"reference" gorm:"type:varchar(255);not null;default:''"`
	Interval  int    `json:"interval" gorm:"type:int;not null;default:60"` // 间隔时间
	// TargetFamily selects the fenced dispatcher; it never enters legacy delivery.
	TargetFamily string `json:"target_family" gorm:"type:varchar(8);not null;default:''"`
	// Additive v1.8 probe-truth fields. Zero/empty on existing rows means the
	// legacy single-probe behavior; new tasks opt in through the admin form.
	ProbeAttempts       int    `json:"probe_attempts" gorm:"type:int;not null;default:0"`
	ProbeSpacingMS      int    `json:"probe_spacing_ms" gorm:"type:int;not null;default:0"`
	ProbeTimeoutMS      int    `json:"probe_timeout_ms" gorm:"type:int;not null;default:0"`
	HTTPMode            string `json:"http_mode" gorm:"type:varchar(16);not null;default:''"`
	HTTPExpectedStatus  string `json:"http_expected_status" gorm:"type:varchar(128);not null;default:''"`
	IPVersionPreference string `json:"ip_version_preference" gorm:"type:varchar(8);not null;default:''"`
	// PinnedGeneration is an internal incarnation, not a hash of the current
	// fields: an edit/revert or removal/reassignment must not revive old work.
	PinnedGeneration string `json:"-" gorm:"type:varchar(32);not null;default:''"`
}

// PingTaskDeletion permanently reserves a deleted task ID. Metrics live in a
// separate store; a nil CleanedAt means their retryable cleanup is still due.
type PingTaskDeletion struct {
	TaskID        uint   `gorm:"primaryKey;autoIncrement:false"`
	ConfigUID     string `gorm:"type:varchar(36);not null;default:'';index"`
	CreatedAt     time.Time
	LastAttemptAt *time.Time `gorm:"index"`
	CleanedAt     *time.Time `gorm:"index"`
}

// AppliesToClient 判断当前 PingTask 是否适用于指定服务器。
func (task PingTask) AppliesToClient(uuid string) bool {
	if uuid == "" {
		return false
	}
	for _, client := range task.Clients {
		if client == uuid {
			return true
		}
	}
	return false
}
