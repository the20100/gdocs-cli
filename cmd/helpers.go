package cmd

// maskOrEmpty returns a masked version of a credential string, or "(not set)" if empty.
func maskOrEmpty(v string) string {
	if v == "" {
		return "(not set)"
	}
	if len(v) <= 8 {
		return "***"
	}
	return v[:4] + "..." + v[len(v)-4:]
}
