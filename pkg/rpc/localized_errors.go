package rpc

// LocalizedErrorData adds a stable presentation key without changing the legacy
// numeric code/message or repurposing another error's data. Keys are not policy.
type LocalizedErrorData struct {
	ErrorKey string `json:"error_key"`
}

func MakeLocalizedError(code int, message, key string) *JsonRpcError {
	return MakeError(code, message, LocalizedErrorData{ErrorKey: key})
}

// LocalizationKey only exposes metadata explicitly supplied by a handler.
func (e *JsonRpcError) LocalizationKey() string {
	if e != nil {
		if data, ok := e.Data.(LocalizedErrorData); ok {
			return data.ErrorKey
		}
	}
	return ""
}
