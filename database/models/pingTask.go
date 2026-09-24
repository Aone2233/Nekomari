package models

import "time"

type PingRecord struct {
	Client     string    `json:"client" gorm:"type:varchar(36);not null;index"`
	ClientInfo Client    `json:"client_info" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	TaskId     uint      `json:"task_id" gorm:"not null;index"`
	Task       PingTask  `json:"task" gorm:"foreignKey:TaskId;references:Id;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;"`
	Time       time.Time `json:"time" gorm:"index;not null"`
	Value      int       `json:"value" gorm:"type:int;not null"` // Ping 值，单位毫秒
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
}

// PingTask 表示一次延迟监测任务配置。
type PingTask struct {
	Id        uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
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
