package tools

func GetInt(m map[string]interface{}, k string, fallback int) int {
	if v, ok := m[k]; ok {
		if v, ok := v.(float64); ok {
			return int(v)
		}
	}
	return fallback
}

func GetStr(m map[string]interface{}, k string, fallback string) string {
	if v, ok := m[k]; ok {
		if v, ok := v.(string); ok {
			return v
		}
	}
	return fallback
}
