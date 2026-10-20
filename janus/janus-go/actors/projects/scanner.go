package projects

import "janus/internal/domain/scan"

// Candidate and ScanResult are the scan rules' types, kept under their old names
// for callers of this package.
type (
	Candidate  = scan.Candidate
	ScanResult = scan.Result
)

func scanFiles(files []fileData) ScanResult {
	input := make([]scan.File, len(files))
	for i, file := range files {
		input[i] = scan.File{Path: file.path, Content: file.content}
	}
	return scan.Files(input)
}
