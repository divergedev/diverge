package webhook

func safeSHA(sha string, maxLen int) string {
	if len(sha) > maxLen {
		return sha[:maxLen]
	}
	return sha
}
