package rpc

// LocalizedErrorData adds a stable presentation key without changing the legacy
// numeric code/message or repurposing another error's data. Keys are not policy.
type LocalizedErrorData struct {
	ErrorKey string `json:"error_key"`
}

// CommittedErrorData is only for mutations known to have committed even
// though a subsequent operation failed. Never use it for ambiguous errors.
type CommittedErrorData struct {
	ErrorKey  string `json:"error_key"`
	Committed bool   `json:"committed"`
	TaskID    uint   `json:"task_id,omitempty"`
}

func MakeLocalizedError(code int, message, key string) *JsonRpcError {
	return MakeError(code, message, LocalizedErrorData{ErrorKey: key})
}

func MakeCommittedError(code int, message, key string, taskID uint) *JsonRpcError {
	return MakeError(code, message, CommittedErrorData{ErrorKey: key, Committed: true, TaskID: taskID})
}

// LocalizationKey only exposes metadata explicitly supplied by a handler.
func (e *JsonRpcError) LocalizationKey() string {
	if e != nil {
		if data, ok := e.Data.(LocalizedErrorData); ok {
			return data.ErrorKey
		}
		if data, ok := e.Data.(CommittedErrorData); ok {
			return data.ErrorKey
		}
	}
	return ""
}
