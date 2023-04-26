package extcontainer

func toStringArray(s interface{}) []string {
	if s == nil {
		return nil
	}

	strings := make([]string, len(s.([]interface{})))
	for i, v := range s.([]interface{}) {
		strings[i] = v.(string)
	}
	return strings
}
