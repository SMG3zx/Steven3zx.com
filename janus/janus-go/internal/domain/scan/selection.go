package scan

// AcceptsEntrypoint reports whether path may be chosen as the entrypoint of the
// snapshot. No entrypoint is always acceptable; any other path must be one of
// the detected candidates.
func (r Result) AcceptsEntrypoint(path string) bool {
	if path == "" {
		return true
	}
	for _, candidate := range r.Candidates {
		if candidate.Path == path {
			return true
		}
	}
	return false
}

// DominantLanguage returns the recognized language with the most files, or ""
// when none was recognized. Ties go to the name that sorts first, so the same
// repository always gets the same language.
func DominantLanguage(languages map[string]int) string {
	best, bestCount := "", 0
	for name, count := range languages {
		if count > bestCount || (count == bestCount && count > 0 && name < best) {
			best, bestCount = name, count
		}
	}
	return best
}
