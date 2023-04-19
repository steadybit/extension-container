package extcontainer

func toStrings(s interface{}) []string {
	if s == nil {
		return nil
	}

	strings := make([]string, len(s.([]interface{})))
	for i, v := range s.([]interface{}) {
		strings[i] = v.(string)
	}
	return strings
}

func uniq[T comparable](s []T) []T {
	if s == nil {
		return nil
	}

	seen := make(map[T]bool)
	var uniq []T
	for _, v := range s {
		if !seen[v] {
			uniq = append(uniq, v)
			seen[v] = true
		}
	}
	return uniq
}
